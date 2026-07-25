package gcplog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/logging/logadmin"
	"github.com/go-errors/errors"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// adcHint is appended to fetch failures so credential problems point people
// at Application Default Credentials setup. LAPP never manages provider auth.
const adcHint = "if this is a credential problem, run `gcloud auth application-default login`"

// FetchRequest describes one pull from GCP Cloud Logging. Filter is the user
// supplied Cloud Logging filter; the time range is combined with it when
// querying. Limit caps the number of returned entries.
type FetchRequest struct {
	Project string
	Filter  string
	From    time.Time
	To      time.Time
	Limit   int
}

// FetchResult carries converted envelope NDJSON lines. Truncated is true
// when the limit cut the result short.
type FetchResult struct {
	Lines     []string
	Truncated bool
}

// FetchLines pulls matching entries from GCP Cloud Logging using Application
// Default Credentials and converts each one to an envelope NDJSON line.
func FetchLines(ctx context.Context, req FetchRequest) (FetchResult, error) {
	client, err := logadmin.NewClient(ctx, req.Project)
	if err != nil {
		return FetchResult{}, errors.Errorf("create cloud logging client (%s): %w", adcHint, err)
	}
	defer func() { _ = client.Close() }()

	it := client.Entries(ctx, logadmin.Filter(CombinedFilter(req.Filter, req.From, req.To)))
	var result FetchResult
	for {
		entry, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return FetchResult{}, errors.Errorf("list cloud logging entries (%s): %w", adcHint, err)
		}
		if req.Limit > 0 && len(result.Lines) >= req.Limit {
			result.Truncated = true
			break
		}
		line, err := ConvertEntry(entryFromLogging(entry))
		if err != nil {
			return FetchResult{}, err
		}
		result.Lines = append(result.Lines, line)
	}
	return result, nil
}

// CombinedFilter joins the resolved time range with the user supplied filter.
// The time range is a separate concept from the filter; they are only
// combined here at query time.
func CombinedFilter(filter string, from, to time.Time) string {
	parts := []string{
		fmt.Sprintf("timestamp >= %q", from.UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf("timestamp <= %q", to.UTC().Format(time.RFC3339Nano)),
	}
	if strings.TrimSpace(filter) != "" {
		parts = append(parts, "("+filter+")")
	}
	return strings.Join(parts, " AND ")
}

// entryFromLogging maps a client library entry onto the neutral Entry shape
// consumed by ConvertEntry.
func entryFromLogging(e *logging.Entry) Entry {
	out := Entry{Timestamp: e.Timestamp}
	if e.Severity != logging.Default {
		out.Severity = strings.ToUpper(e.Severity.String())
	}
	switch payload := e.Payload.(type) {
	case nil:
	case string:
		out.TextPayload = &payload
	case proto.Message:
		if data, err := protojson.Marshal(payload); err == nil {
			out.JSONPayload = data
		} else {
			text := fmt.Sprint(payload)
			out.TextPayload = &text
		}
	default:
		if data, err := json.Marshal(payload); err == nil {
			out.JSONPayload = data
		} else {
			text := fmt.Sprint(payload)
			out.TextPayload = &text
		}
	}
	return out
}
