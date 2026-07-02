// Package gcplog fetches log entries from Google Cloud Logging and renders
// them as plain text lines suitable for the workspace discovery pipeline.
package gcplog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/logging/logadmin"
	"github.com/go-errors/errors"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

// progressInterval controls how many entries are fetched between
// OnProgress callbacks.
const progressInterval = 500

// FetchConfig configures a single fetch from Cloud Logging.
type FetchConfig struct {
	// ProjectID is the GCP project to read logs from.
	ProjectID string
	// Filter is an optional Cloud Logging filter expression. It is combined
	// with the time range using AND.
	Filter string
	// Since is the inclusive lower bound of the time range.
	Since time.Time
	// Until is the optional inclusive upper bound. Zero means now.
	Until time.Time
	// Limit caps the number of fetched entries and must be positive.
	Limit int
	// OnProgress is invoked periodically with the number of entries
	// fetched so far.
	OnProgress func(fetched int)
}

func (c FetchConfig) validate() error {
	if c.ProjectID == "" {
		return errors.New("gcp project id is required")
	}
	if c.Since.IsZero() {
		return errors.New("since time is required")
	}
	if c.Limit <= 0 {
		return errors.New("limit must be positive")
	}
	if !c.Until.IsZero() && c.Until.Before(c.Since) {
		return errors.New("until must not be before since")
	}
	return nil
}

// Fetcher fetches Cloud Logging entries and writes rendered text lines.
type Fetcher interface {
	Fetch(ctx context.Context, cfg FetchConfig, w io.Writer) (int, error)
}

var _ Fetcher = (*logadminFetcher)(nil)

// logadminFetcher reads entries through the Cloud Logging admin API using
// Application Default Credentials.
type logadminFetcher struct{}

// NewFetcher returns a Fetcher backed by the Cloud Logging admin API.
func NewFetcher() Fetcher {
	return &logadminFetcher{}
}

func (f *logadminFetcher) Fetch(ctx context.Context, cfg FetchConfig, w io.Writer) (int, error) {
	if err := cfg.validate(); err != nil {
		return 0, err
	}
	client, err := logadmin.NewClient(ctx, cfg.ProjectID)
	if err != nil {
		return 0, errors.Errorf("create cloud logging client: %w", err)
	}
	defer func() { _ = client.Close() }()

	it := client.Entries(ctx, logadmin.Filter(BuildFilter(cfg)))
	return drainEntries(it, cfg, w)
}

// entryIterator abstracts logadmin's entry iterator for testing.
type entryIterator interface {
	Next() (*logging.Entry, error)
}

func drainEntries(it entryIterator, cfg FetchConfig, w io.Writer) (int, error) {
	count := 0
	for count < cfg.Limit {
		entry, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return count, errors.Errorf("fetch cloud logging entries: %w", err)
		}
		if _, err := io.WriteString(w, RenderEntry(entry)+"\n"); err != nil {
			return count, errors.Errorf("write log line: %w", err)
		}
		count++
		if cfg.OnProgress != nil && count%progressInterval == 0 {
			cfg.OnProgress(count)
		}
	}
	return count, nil
}

// BuildFilter combines the configured time range and user filter into a
// single Cloud Logging filter expression.
func BuildFilter(cfg FetchConfig) string {
	parts := []string{fmt.Sprintf("timestamp>=%q", cfg.Since.UTC().Format(time.RFC3339Nano))}
	if !cfg.Until.IsZero() {
		parts = append(parts, fmt.Sprintf("timestamp<=%q", cfg.Until.UTC().Format(time.RFC3339Nano)))
	}
	if strings.TrimSpace(cfg.Filter) != "" {
		parts = append(parts, "("+strings.TrimSpace(cfg.Filter)+")")
	}
	return strings.Join(parts, " AND ")
}

// RenderEntry renders one Cloud Logging entry as a single text line with an
// RFC3339 timestamp prefix so the multiline detector recognizes entry
// boundaries.
func RenderEntry(entry *logging.Entry) string {
	ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
	severity := strings.ToUpper(entry.Severity.String())
	payload := renderPayload(entry.Payload)
	return ts + " " + severity + " " + payload
}

func renderPayload(payload any) string {
	switch value := payload.(type) {
	case nil:
		return ""
	case string:
		return value
	case *structpb.Struct:
		if message := structMessage(value); message != "" {
			return message
		}
		data, err := value.MarshalJSON()
		if err != nil {
			return value.String()
		}
		return string(data)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return string(data)
	}
}

// structMessage extracts a conventional message field from a JSON payload.
func structMessage(payload *structpb.Struct) string {
	for _, key := range []string{"message", "msg"} {
		if field, ok := payload.GetFields()[key]; ok {
			if text := field.GetStringValue(); text != "" {
				return text
			}
		}
	}
	return ""
}
