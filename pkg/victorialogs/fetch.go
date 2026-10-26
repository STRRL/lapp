// Package victorialogs pulls log entries from a VictoriaLogs instance over
// its LogsQL HTTP API. VictoriaLogs already returns one JSON object per line
// with `_msg` and `_time`, so lines are written to the workspace as returned.
package victorialogs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Provider is the ImportRun provider name for VictoriaLogs.
const Provider = "victorialogs"

// authHint is appended to authorization failures. LAPP never manages
// provider auth; credentials come from the environment.
const authHint = "if this is a credential problem, set VICTORIALOGS_BEARER_TOKEN, or VICTORIALOGS_USERNAME and VICTORIALOGS_PASSWORD"

const (
	requestTimeout = 5 * time.Minute
	maxAttempts    = 3
	maxLineBytes   = 16 << 20
	maxErrorBytes  = 1 << 10
)

// retryDelay is the wait before retry attempt+1; tests override it.
var retryDelay = func(attempt int) time.Duration {
	return time.Duration(attempt) * time.Second
}

// Auth holds credentials for a VictoriaLogs instance: a bearer token, or a
// username and password for basic auth. Zero value means no auth.
type Auth struct {
	BearerToken string
	Username    string
	Password    string
}

// Validate rejects ambiguous credentials.
func (a Auth) Validate() error {
	if a.BearerToken != "" && (a.Username != "" || a.Password != "") {
		return errors.New("set either a VictoriaLogs bearer token or a username and password, not both")
	}
	if a.Password != "" && a.Username == "" {
		return errors.New("a VictoriaLogs password needs a username")
	}
	return nil
}

// Client talks to VictoriaLogs. HTTPClient is optional; the default uses an
// otelhttp transport and a per-request timeout.
type Client struct {
	Auth       Auth
	HTTPClient *http.Client
}

// FetchRequest describes one pull from VictoriaLogs. Query is a LogsQL
// filter; the time range is sent separately as start and end. Tenant is
// optional, as "<AccountID>:<ProjectID>". Limit caps the returned entries.
type FetchRequest struct {
	URL    string
	Tenant string
	Query  string
	From   time.Time
	To     time.Time
	Limit  int
}

// FetchResult reports facts about one completed fetch.
type FetchResult struct {
	Truncated bool
}

// Validate checks the request before any network access.
func (r FetchRequest) Validate() error {
	if err := ValidateURL(r.URL); err != nil {
		return err
	}
	if _, _, err := ParseTenant(r.Tenant); err != nil {
		return err
	}
	if r.From.After(r.To) {
		return errors.New("from must not be after to")
	}
	if r.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

// ValidateURL accepts an http(s) base URL without credentials, query, or
// fragment, so the URL is safe to store in the ImportRun record.
func ValidateURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("VictoriaLogs URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.Errorf("invalid VictoriaLogs URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.Errorf("invalid VictoriaLogs URL %q: expected http(s)://host[:port]", raw)
	}
	if u.User != nil {
		return errors.New("VictoriaLogs URL must not contain credentials; set VICTORIALOGS_BEARER_TOKEN or VICTORIALOGS_USERNAME and VICTORIALOGS_PASSWORD")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.Errorf("VictoriaLogs URL %q must not contain a query or fragment", raw)
	}
	return nil
}

// ParseTenant splits "<AccountID>:<ProjectID>". An empty tenant is the
// default tenant and sends no tenant headers.
func ParseTenant(tenant string) (accountID, projectID string, err error) {
	if tenant == "" {
		return "", "", nil
	}
	accountID, projectID, ok := strings.Cut(tenant, ":")
	if !ok {
		return "", "", errors.Errorf("invalid VictoriaLogs tenant %q, expected <AccountID>:<ProjectID>", tenant)
	}
	for _, id := range []string{accountID, projectID} {
		if _, err := strconv.ParseUint(id, 10, 32); err != nil {
			return "", "", errors.Errorf("invalid VictoriaLogs tenant %q, expected numeric <AccountID>:<ProjectID>", tenant)
		}
	}
	return accountID, projectID, nil
}

// Fetch streams matching entries from /select/logsql/query and sends each
// NDJSON line to writeLine. The response is read line by line, never held
// in memory as a whole.
func (c *Client) Fetch(ctx context.Context, req FetchRequest, writeLine func(string) error) (FetchResult, error) {
	if writeLine == nil {
		return FetchResult{}, errors.New("log line writer is required")
	}
	if err := req.Validate(); err != nil {
		return FetchResult{}, err
	}
	form := timeRangeForm(req)
	form.Set("query", queryOrAll(req.Query))
	if req.Limit > 0 {
		// One extra entry tells a result that fits exactly from a truncated one.
		form.Set("limit", strconv.Itoa(req.Limit+1))
	}
	resp, err := c.post(ctx, req, form)
	if err != nil {
		return FetchResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result FetchResult
	count := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if req.Limit > 0 && count >= req.Limit {
			result.Truncated = true
			break
		}
		if err := writeLine(string(line)); err != nil {
			return FetchResult{}, errors.Errorf("write victorialogs entry: %w", err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return FetchResult{}, errors.Errorf("read victorialogs response: %w", err)
	}
	return result, nil
}

// Stream is one VictoriaLogs log stream that has matching entries.
type Stream struct {
	ID     string
	Labels string
	Hits   int
}

// Streams lists the streams with entries matching the query and time range.
func (c *Client) Streams(ctx context.Context, req FetchRequest) ([]Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	form := timeRangeForm(req)
	form.Set("query", "("+queryOrAll(req.Query)+") | stats by (_stream_id, _stream) count() hits")
	resp, err := c.post(ctx, req, form)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var streams []Stream
	decoder := json.NewDecoder(resp.Body)
	for {
		var row struct {
			ID     string `json:"_stream_id"`
			Labels string `json:"_stream"`
			Hits   string `json:"hits"`
		}
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.Errorf("decode victorialogs streams: %w", err)
		}
		hits, err := strconv.Atoi(row.Hits)
		if err != nil {
			return nil, errors.Errorf("decode victorialogs stream hits %q: %w", row.Hits, err)
		}
		streams = append(streams, Stream{ID: row.ID, Labels: row.Labels, Hits: hits})
	}
	return streams, nil
}

func timeRangeForm(req FetchRequest) url.Values {
	form := url.Values{}
	form.Set("start", req.From.UTC().Format(time.RFC3339Nano))
	form.Set("end", req.To.UTC().Format(time.RFC3339Nano))
	return form
}

func queryOrAll(query string) string {
	if strings.TrimSpace(query) == "" {
		return "*"
	}
	return query
}

// post sends the query with bounded retries on transport errors, 429, and
// 5xx. Retries only happen before any response body is consumed.
func (c *Client) post(ctx context.Context, req FetchRequest, form url.Values) (*http.Response, error) {
	if err := c.Auth.Validate(); err != nil {
		return nil, err
	}
	accountID, projectID, err := ParseTenant(req.Tenant)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(req.URL, "/") + "/select/logsql/query"
	body := form.Encode()
	for attempt := 1; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			return nil, errors.Errorf("create victorialogs request: %w", err)
		}
		c.setHeaders(httpReq, accountID, projectID)

		resp, retryable, err := c.do(httpReq)
		if err == nil {
			return resp, nil
		}
		if !retryable || attempt == maxAttempts || ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, errors.Errorf("query victorialogs: %w", ctx.Err())
		case <-time.After(retryDelay(attempt)):
		}
	}
}

func (c *Client) setHeaders(httpReq *http.Request, accountID, projectID string) {
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accountID != "" {
		httpReq.Header.Set("AccountID", accountID)
		httpReq.Header.Set("ProjectID", projectID)
	}
	switch {
	case c.Auth.BearerToken != "":
		httpReq.Header.Set("Authorization", "Bearer "+c.Auth.BearerToken)
	case c.Auth.Username != "":
		httpReq.SetBasicAuth(c.Auth.Username, c.Auth.Password)
	}
}

// do sends one attempt and reports whether a failure is worth retrying.
func (c *Client) do(httpReq *http.Request) (resp *http.Response, retryable bool, err error) {
	resp, err = c.httpClient().Do(httpReq)
	if err != nil {
		return nil, true, errors.Errorf("query victorialogs: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, false, nil
	}
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return nil, retryable, statusError(resp)
}

func statusError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	status := resp.Status
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		status += " (" + authHint + ")"
	}
	if message := strings.TrimSpace(string(data)); message != "" {
		return errors.Errorf("query victorialogs: %s: %s", status, message)
	}
	return errors.Errorf("query victorialogs: %s", status)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   requestTimeout,
	}
}
