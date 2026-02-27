package ingestor

import (
	"bufio"
	"context"
	"os"

	"github.com/go-errors/errors"
)

// LogLine represents a single raw log line read from input.
type LogLine struct {
	LineNumber int
	Content    string
}

// Result wraps either a successfully read value or a read error,
// similar to Result<T, E> in Rust.
type Result[T any] struct {
	Value T
	Err   error
}

// Ingestor reads log lines from a source and streams them as Results.
type Ingestor interface {
	Ingest(ctx context.Context) (<-chan Result[*LogLine], error)
}

var _ Ingestor = (*FileIngestor)(nil)

// FileIngestor reads log lines from a file path or stdin.
type FileIngestor struct {
	Path string
}

// Ingest reads log lines from the file (or stdin if Path is "-").
// Cancel the context to stop reading early; the goroutine will exit promptly.
func (f *FileIngestor) Ingest(ctx context.Context) (<-chan Result[*LogLine], error) {
	var file *os.File
	if f.Path == "-" {
		file = os.Stdin
	} else {
		var err error
		file, err = os.Open(f.Path)
		if err != nil {
			return nil, errors.Errorf("open log file: %w", err)
		}
	}

	ownFile := f.Path != "-"
	ch := make(chan Result[*LogLine], 100)
	go func() {
		defer close(ch)

		var fileErr error
		defer func() {
			if ownFile {
				if cerr := file.Close(); cerr != nil {
					fileErr = errors.Join(fileErr, errors.Errorf("close log file: %w", cerr))
				}
			}
			if fileErr != nil {
				select {
				case ch <- Result[*LogLine]{Err: fileErr}:
				case <-ctx.Done():
				}
			}
		}()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			select {
			case ch <- Result[*LogLine]{Value: &LogLine{LineNumber: lineNum, Content: scanner.Text()}}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			fileErr = errors.Errorf("read log file: %w", err)
		}
	}()

	return ch, nil
}

// Ingest is a convenience function that creates a FileIngestor and reads from it.
// Pass "-" to read from stdin.
func Ingest(ctx context.Context, filePath string) (<-chan Result[*LogLine], error) {
	return (&FileIngestor{Path: filePath}).Ingest(ctx)
}
