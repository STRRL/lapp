package store

import "time"

// LogEntry represents a single stored log line.
type LogEntry struct {
	ID         int64
	LineNumber int
	Timestamp  time.Time
	Raw        string
	TemplateID string
	Template   string
}

// TemplateSummary holds a template and its occurrence count.
type TemplateSummary struct {
	TemplateID string
	Template   string
	Count      int
}

// QueryOpts specifies filters for querying log entries.
type QueryOpts struct {
	TemplateID string
	From       time.Time
	To         time.Time
	Limit      int
}

// Store persists log entries and templates.
type Store interface {
	// Init creates tables if they don't exist.
	Init() error
	// InsertLog stores a parsed log entry.
	InsertLog(entry LogEntry) error
	// InsertLogBatch stores multiple log entries.
	InsertLogBatch(entries []LogEntry) error
	// QueryByTemplate returns entries matching a template ID.
	QueryByTemplate(templateID string) ([]LogEntry, error)
	// QueryLogs returns entries matching the given options.
	QueryLogs(opts QueryOpts) ([]LogEntry, error)
	// TemplateSummaries returns all templates with their counts.
	TemplateSummaries() ([]TemplateSummary, error)
	// Close releases resources.
	Close() error
}
