package store

import (
	"context"
	"time"
)

// LogEntry represents a single stored log line.
type LogEntry struct {
	ID         int64
	LineNumber int
	Timestamp  time.Time
	Raw        string
	PatternID  string
}

// Pattern represents a discovered log pattern with optional semantic labels.
type Pattern struct {
	PatternID   string
	PatternType string
	RawPattern  string
	SemanticID  string
	Description string
}

// PatternSummary holds a pattern and its occurrence count.
type PatternSummary struct {
	PatternID   string
	Pattern     string
	Count       int
	PatternType string
	SemanticID  string
	Description string
}

// QueryOpts specifies filters for querying log entries.
type QueryOpts struct {
	PatternID string
	From      time.Time
	To        time.Time
	Limit     int
}

// Store persists log entries and patterns.
type Store interface {
	// Init creates tables if they don't exist.
	Init(ctx context.Context) error
	// InsertLog stores a parsed log entry.
	InsertLog(ctx context.Context, entry LogEntry) error
	// InsertLogBatch stores multiple log entries.
	InsertLogBatch(ctx context.Context, entries []LogEntry) error
	// QueryByPattern returns entries matching a pattern ID.
	QueryByPattern(ctx context.Context, patternID string) ([]LogEntry, error)
	// QueryLogs returns entries matching the given options.
	QueryLogs(ctx context.Context, opts QueryOpts) ([]LogEntry, error)
	// PatternSummaries returns all patterns with their counts.
	PatternSummaries(ctx context.Context) ([]PatternSummary, error)
	// InsertPatterns upserts patterns into the patterns table.
	InsertPatterns(ctx context.Context, patterns []Pattern) error
	// Patterns returns all patterns.
	Patterns(ctx context.Context) ([]Pattern, error)
	// UpdatePatternLabels updates only semantic_id and description for patterns.
	UpdatePatternLabels(ctx context.Context, labels []Pattern) error
	// ClearOrphanPatternIDs sets pattern_id to empty for log entries
	// whose pattern_id does not exist in the patterns table.
	ClearOrphanPatternIDs(ctx context.Context) (int64, error)
	// PatternCounts returns the number of log entries per pattern_id.
	PatternCounts(ctx context.Context) (map[string]int, error)
	// Close releases resources.
	Close() error
}
