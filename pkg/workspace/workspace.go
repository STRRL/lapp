package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// TaggedLine represents a log line with its source file and line number.
type TaggedLine struct {
	Content  string
	FileName string
	LineNum  int
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

// ReadAllLogs reads all log files in <dir>/logs/ and returns a map of filename to lines.
func ReadAllLogs(dir string) (map[string][]string, error) {
	logsDir := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lines, err := readFileLines(filepath.Join(logsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		result[e.Name()] = lines
	}
	return result, nil
}

func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Trim trailing empty lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}
