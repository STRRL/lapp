package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strrl/lapp/pkg/logsource"
)

// TaggedLine represents a log line with its source file and line number.
type TaggedLine struct {
	Content  string
	FileName string
	LineNum  int
	// ExtractedLine is the text fed to pattern mining: the message for NDJSON
	// entries, or the body after the timestamp/host header for plain text.
	// Empty when Content is mined directly.
	ExtractedLine string `json:",omitempty"`
	// Timestamp is parsed from the line header or the NDJSON timestamp field
	// when one is recognised.
	Timestamp *time.Time `json:",omitempty"`
}

// DrainLine returns the text used for pattern mining and template matching:
// the extracted line for NDJSON entries, otherwise the raw content.
func (t TaggedLine) DrainLine() string {
	if t.ExtractedLine != "" {
		return t.ExtractedLine
	}
	return t.Content
}

// LineRef identifies a line's location in a source file.
type LineRef struct {
	FileName string
	LineNum  int
}

// PatternInfo holds all data about a discovered pattern for workspace output.
type PatternInfo struct {
	SemanticID  string
	DirName     string
	Template    string
	Description string
	Count       int
	FirstSeen   LineRef
	LastSeen    LineRef
	LineRefs    []LineRef
	Samples     []string
}

// ListLogFiles returns the basenames of all files in <dir>/logs/.
func ListLogFiles(dir string) ([]string, error) {
	logsDir := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// ReadAllLogs reads all log files in <dir>/logs/ and returns a map of filename
// to lines, plus how many lines were cut at logsource.MaxLineBytes.
func ReadAllLogs(dir string) (map[string][]string, int, error) {
	logsDir := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, 0, err
	}
	result := make(map[string][]string)
	truncatedLines := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lines, truncated, err := readFileLines(filepath.Join(logsDir, e.Name()))
		if err != nil {
			return nil, 0, err
		}
		result[e.Name()] = lines
		truncatedLines += truncated
	}
	return result, truncatedLines, nil
}

func readFileLines(path string) ([]string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	truncatedLines := 0
	err = logsource.ScanLines(f, func(line string, truncated bool) error {
		lines = append(lines, line)
		if truncated {
			truncatedLines++
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	// Trim trailing empty lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, truncatedLines, nil
}
