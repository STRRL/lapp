package parser

// Template represents a discovered log template.
type Template struct {
	ID      string
	Pattern string
}

// Result holds the outcome of parsing a single log line.
type Result struct {
	Matched   bool
	PatternID string
	Pattern   string
	Params    map[string]string
}

// Parser discovers and matches log templates.
type Parser interface {
	// Parse processes a log line and returns the matching result.
	Parse(content string) Result
	// Templates returns all discovered templates so far.
	Templates() []Template
}
