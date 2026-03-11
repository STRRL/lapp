package ingest

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/event"
	"github.com/strrl/lapp/pkg/logsource"
	"github.com/strrl/lapp/pkg/store"
)

type stubParser struct {
	parse func(context.Context, string) (*ParseResult, error)
}

func (s stubParser) Parse(ctx context.Context, raw string) (*ParseResult, error) {
	return s.parse(ctx, raw)
}

func newTestStore(t *testing.T) *store.DuckDBStore {
	t.Helper()

	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatalf("NewDuckDBStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return s
}

func TestFromRawLineWithoutParserUsesFallbackEvent(t *testing.T) {
	line := &logsource.LogLine{
		LineNumber: 7,
		Content:    "  WARN worker stalled for 30s  ",
	}

	outcome, err := FromRawLine(context.Background(), line, nil)
	if err != nil {
		t.Fatalf("FromRawLine: %v", err)
	}

	if outcome.ParseErr != nil {
		t.Fatalf("ParseErr: got %v, want nil", outcome.ParseErr)
	}
	if outcome.Event.Text != line.Content {
		t.Fatalf("Event.Text: got %q, want %q", outcome.Event.Text, line.Content)
	}
	if len(outcome.Event.Attrs) != 0 {
		t.Fatalf("Event.Attrs: got %v, want empty map", outcome.Event.Attrs)
	}
	if outcome.Event.Inferred == nil {
		t.Fatal("Event.Inferred: got nil, want non-nil empty inferred")
	}
	if outcome.LogEntry.Raw != line.Content {
		t.Fatalf("LogEntry.Raw: got %q, want %q", outcome.LogEntry.Raw, line.Content)
	}
	if outcome.LogEntry.LineNumber != line.LineNumber || outcome.LogEntry.EndLineNumber != line.LineNumber {
		t.Fatalf("log entry line bounds: got (%d,%d), want (%d,%d)", outcome.LogEntry.LineNumber, outcome.LogEntry.EndLineNumber, line.LineNumber, line.LineNumber)
	}
}

func TestStoreRawLinePersistsWhenParserFails(t *testing.T) {
	s := newTestStore(t)
	line := &logsource.LogLine{
		LineNumber: 12,
		Content:    "ERROR checkout request req_123 timed out",
	}

	outcome, err := StoreRawLine(context.Background(), s, line, stubParser{
		parse: func(context.Context, string) (*ParseResult, error) {
			return nil, context.DeadlineExceeded
		},
	})
	if err != nil {
		t.Fatalf("StoreRawLine: %v", err)
	}
	if !stderrors.Is(outcome.ParseErr, context.DeadlineExceeded) {
		t.Fatalf("ParseErr: got %v, want %v", outcome.ParseErr, context.DeadlineExceeded)
	}
	if outcome.Event.Text != line.Content {
		t.Fatalf("Event.Text: got %q, want %q", outcome.Event.Text, line.Content)
	}

	stored, err := s.QueryLogs(context.Background(), store.QueryOpts{})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored entries: got %d, want 1", len(stored))
	}
	if stored[0].Raw != line.Content {
		t.Fatalf("stored raw: got %q, want %q", stored[0].Raw, line.Content)
	}
}

func TestStoreRawLineUsesParsedFieldsButKeepsRawText(t *testing.T) {
	s := newTestStore(t)
	ts := time.Date(2026, 3, 10, 21, 0, 0, 0, time.UTC)
	line := &logsource.LogLine{
		LineNumber: 3,
		Content:    "level=info service=auth-api msg=\"user user_456 authenticated\"",
	}

	outcome, err := StoreRawLine(context.Background(), s, line, stubParser{
		parse: func(context.Context, string) (*ParseResult, error) {
			return &ParseResult{
				Timestamp: &ts,
				Attrs: map[string]string{
					"level":   "info",
					"service": "auth-api",
				},
				Inferred: &event.Inferred{
					Pattern: "user <*> authenticated",
					Entity:  "auth-api",
				},
				Labels: map[string]string{
					"pattern": "user-authenticated",
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("StoreRawLine: %v", err)
	}

	if outcome.Event.Text != line.Content {
		t.Fatalf("Event.Text: got %q, want %q", outcome.Event.Text, line.Content)
	}
	if outcome.Event.Timestamp == nil || !outcome.Event.Timestamp.Equal(ts) {
		t.Fatalf("Event.Timestamp: got %v, want %v", outcome.Event.Timestamp, ts)
	}
	if outcome.Event.Attrs["service"] != "auth-api" {
		t.Fatalf("Event.Attrs[service]: got %q, want %q", outcome.Event.Attrs["service"], "auth-api")
	}
	if outcome.Event.Inferred == nil || outcome.Event.Inferred.Pattern != "user <*> authenticated" {
		t.Fatalf("Event.Inferred: got %+v", outcome.Event.Inferred)
	}
	if outcome.LogEntry.Raw != line.Content {
		t.Fatalf("LogEntry.Raw: got %q, want %q", outcome.LogEntry.Raw, line.Content)
	}
	if outcome.LogEntry.Timestamp != ts {
		t.Fatalf("LogEntry.Timestamp: got %v, want %v", outcome.LogEntry.Timestamp, ts)
	}
	if outcome.LogEntry.Labels["pattern"] != "user-authenticated" {
		t.Fatalf("LogEntry.Labels[pattern]: got %q, want %q", outcome.LogEntry.Labels["pattern"], "user-authenticated")
	}

	stored, err := s.QueryLogs(context.Background(), store.QueryOpts{})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored entries: got %d, want 1", len(stored))
	}
	if stored[0].Raw != line.Content {
		t.Fatalf("stored raw: got %q, want %q", stored[0].Raw, line.Content)
	}
	if stored[0].Labels["pattern"] != "user-authenticated" {
		t.Fatalf("stored labels[pattern]: got %q, want %q", stored[0].Labels["pattern"], "user-authenticated")
	}
}
