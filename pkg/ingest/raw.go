package ingest

import (
	"context"
	"time"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/event"
	"github.com/strrl/lapp/pkg/logsource"
	"github.com/strrl/lapp/pkg/store"
)

// ParseResult carries parser-derived event data plus optional storage labels.
type ParseResult struct {
	Timestamp *time.Time
	Attrs     map[string]string
	Inferred  *event.Inferred
	Labels    map[string]string
}

// Parser extracts structured data from a raw log line.
type Parser interface {
	Parse(ctx context.Context, raw string) (*ParseResult, error)
}

// Outcome captures the normalized event, the storage record, and any parser error.
type Outcome struct {
	Event    event.Event
	LogEntry store.LogEntry
	ParseErr error
}

// FromRawLine normalizes a raw log line and keeps the original text as the source of truth.
// Parser failures are surfaced on the Outcome but still return a fallback event/log entry.
func FromRawLine(ctx context.Context, line *logsource.LogLine, parser Parser) (Outcome, error) {
	if line == nil {
		return Outcome{}, errors.New("raw log line is required")
	}

	outcome := Outcome{
		Event: event.Event{
			Text:     line.Content,
			Attrs:    map[string]string{},
			Inferred: &event.Inferred{},
		},
		LogEntry: store.LogEntry{
			LineNumber:    line.LineNumber,
			EndLineNumber: line.LineNumber,
			Raw:           line.Content,
			Labels:        map[string]string{},
		},
	}

	if parser == nil {
		return outcome, nil
	}

	parsed, parseErr := parser.Parse(ctx, line.Content)
	if parseErr != nil {
		outcome.ParseErr = parseErr
	} else if parsed != nil {
		if parsed.Timestamp != nil {
			ts := *parsed.Timestamp
			outcome.Event.Timestamp = &ts
			outcome.LogEntry.Timestamp = ts
		}
		if parsed.Attrs != nil {
			outcome.Event.Attrs = cloneMap(parsed.Attrs)
		}
		if parsed.Inferred != nil {
			outcome.Event.Inferred = &event.Inferred{
				Pattern: parsed.Inferred.Pattern,
				Entity:  parsed.Inferred.Entity,
			}
		}
		if parsed.Labels != nil {
			outcome.LogEntry.Labels = cloneMap(parsed.Labels)
		}
	}

	return outcome, nil
}

// StoreRawLine persists the raw line outcome. Parser failures never prevent storage.
func StoreRawLine(ctx context.Context, dst store.Store, line *logsource.LogLine, parser Parser) (Outcome, error) {
	if dst == nil {
		return Outcome{}, errors.New("store is required")
	}

	outcome, err := FromRawLine(ctx, line, parser)
	if err != nil {
		return Outcome{}, err
	}
	if err := dst.InsertLog(ctx, outcome.LogEntry); err != nil {
		return Outcome{}, errors.Errorf("insert raw log line: %w", err)
	}
	return outcome, nil
}

func cloneMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	cloned := make(map[string]string, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}
