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
	// Truncated marks an entry with a line cut at logsource.MaxLineBytes.
	Truncated bool `json:",omitempty"`
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

// LogFileLines holds one log file's lines and which of them were cut at
// logsource.MaxLineBytes.
type LogFileLines struct {
	Lines []string
	// Truncated holds the 0-based indexes of lines that were cut.
	Truncated map[int]bool
}

// ReadAllLogs reads all log files in <dir>/logs/ and returns their lines by
// file name.
func ReadAllLogs(dir string) (map[string]LogFileLines, error) {
	logsDir := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]LogFileLines)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		file, err := readFileLines(filepath.Join(logsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		result[e.Name()] = file
	}
	return result, nil
}

func readFileLines(path string) (LogFileLines, error) {
	f, err := os.Open(path)
	if err != nil {
		return LogFileLines{}, err
	}
	defer func() { _ = f.Close() }()

	var file LogFileLines
	err = logsource.ScanLines(f, func(line string, truncated bool) error {
		if truncated {
			if file.Truncated == nil {
				file.Truncated = map[int]bool{}
			}
			file.Truncated[len(file.Lines)] = true
		}
		file.Lines = append(file.Lines, line)
		return nil
	})
	if err != nil {
		return LogFileLines{}, err
	}

	// Trim trailing empty lines
	for len(file.Lines) > 0 && strings.TrimSpace(file.Lines[len(file.Lines)-1]) == "" {
		file.Lines = file.Lines[:len(file.Lines)-1]
	}
	return file, nil
}
