package ingestor

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// LogLine represents a single raw log line read from input.
type LogLine struct {
	LineNumber int
	Content    string
}

// Ingest reads log lines from a file path.
// Use "-" to read from stdin.
func Ingest(filePath string) (<-chan LogLine, error) {
	var reader io.Reader

	if filePath == "-" {
		reader = os.Stdin
	} else {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		reader = f
	}

	ch := make(chan LogLine, 100)
	go func() {
		defer close(ch)
		if closer, ok := reader.(io.Closer); ok && filePath != "-" {
			defer func() { _ = closer.Close() }()
		}
		scanner := bufio.NewScanner(reader)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			ch <- LogLine{
				LineNumber: lineNum,
				Content:    scanner.Text(),
			}
		}
	}()

	return ch, nil
}
