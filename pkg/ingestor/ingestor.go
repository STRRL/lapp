package ingestor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
)

// LogLine represents a single raw log line read from input.
// If Err is non-nil, the line signals a read error and Content is empty.
type LogLine struct {
	LineNumber int
	Content    string
	Err        error
}

// Ingest reads log lines from a file path.
// Cancel the context to stop reading early; the goroutine will exit promptly.
func Ingest(ctx context.Context, filePath string) (<-chan LogLine, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	ch := make(chan LogLine, 100)
	go func() {
		defer close(ch)

		var fileErr error
		defer func() {
			if cerr := f.Close(); cerr != nil {
				fileErr = errors.Join(fileErr, fmt.Errorf("close log file: %w", cerr))
			}
			if fileErr != nil {
				select {
				case ch <- LogLine{Err: fileErr}:
				case <-ctx.Done():
				}
			}
		}()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			select {
			case ch <- LogLine{LineNumber: lineNum, Content: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			fileErr = fmt.Errorf("read log file: %w", err)
		}
	}()

	return ch, nil
}
