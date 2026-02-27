package ingestor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
)

// LogLine represents a single raw log line read from input.
type LogLine struct {
	LineNumber int
	Content    string
}

// ReadResult wraps either a successfully read LogLine or a read error,
// similar to Result<T, E> in Rust.
type ReadResult struct {
	Line *LogLine
	Err  error
}

// Ingestor reads log lines from a source and streams them as ReadResults.
type Ingestor interface {
	Ingest(ctx context.Context) (<-chan ReadResult, error)
}

// FileIngestor reads log lines from a file path or stdin.
type FileIngestor struct {
	Path string
}

// Ingest reads log lines from the file (or stdin if Path is "-").
// Cancel the context to stop reading early; the goroutine will exit promptly.
func (f *FileIngestor) Ingest(ctx context.Context) (<-chan ReadResult, error) {
	var file *os.File
	if f.Path == "-" {
		file = os.Stdin
	} else {
		var err error
		file, err = os.Open(f.Path)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
	}

	ownFile := f.Path != "-"
	ch := make(chan ReadResult, 100)
	go func() {
		defer close(ch)

		var fileErr error
		defer func() {
			if ownFile {
				if cerr := file.Close(); cerr != nil {
					fileErr = errors.Join(fileErr, fmt.Errorf("close log file: %w", cerr))
				}
			}
			if fileErr != nil {
				select {
				case ch <- ReadResult{Err: fileErr}:
				case <-ctx.Done():
				}
			}
		}()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			select {
			case ch <- ReadResult{Line: &LogLine{LineNumber: lineNum, Content: scanner.Text()}}:
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

// Ingest is a convenience function that creates a FileIngestor and reads from it.
// Pass "-" to read from stdin.
func Ingest(ctx context.Context, filePath string) (<-chan ReadResult, error) {
	return (&FileIngestor{Path: filePath}).Ingest(ctx)
}
