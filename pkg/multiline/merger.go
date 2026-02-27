// TODO: Consider upgrading to Fluent Bit-style state machine for
// language-specific stack trace parsing (Java `Caused by:` chains,
// Python `File "..."` frames, Go goroutine dumps). The current
// timestamp-only approach handles 99%+ of cases but can't semantically
// understand continuation structure.

package multiline

import (
	"strings"

	"github.com/strrl/lapp/pkg/ingestor"
)

// MergedLine represents one logical log entry that may span multiple
// physical lines.
type MergedLine struct {
	StartLine int
	EndLine   int
	Content   string
}

// MergeResult wraps either a successfully merged line or an error from the input stream.
type MergeResult struct {
	Value *MergedLine
	Err   error
}

// Merge reads physical lines from in and merges continuation lines into
// logical entries using the provided detector to identify entry boundaries.
// It propagates read errors from the ingestor Result channel.
// If no line is ever detected as a new entry (i.e. no recognizable timestamp),
// each physical line is emitted as its own entry to avoid behavioral regression.
func Merge(in <-chan ingestor.Result[*ingestor.LogLine], detector *Detector) <-chan MergeResult {
	out := make(chan MergeResult, 100)
	go func() {
		defer close(out)

		var buf []string
		startLine := 0
		endLine := 0
		bufBytes := 0
		everDetected := false

		flush := func() {
			if len(buf) == 0 {
				return
			}
			out <- MergeResult{
				Value: &MergedLine{
					StartLine: startLine,
					EndLine:   endLine,
					Content:   strings.Join(buf, "\n"),
				},
			}
			buf = buf[:0]
			bufBytes = 0
		}

		for rr := range in {
			if rr.Err != nil {
				out <- MergeResult{Err: rr.Err}
				return
			}
			line := rr.Value
			isNew := detector.IsNewEntry(line.Content)
			if isNew {
				everDetected = true
			}

			// If we have never seen a timestamp, fall back to line-by-line
			if !everDetected && len(buf) > 0 {
				flush()
			}

			if isNew && len(buf) > 0 {
				flush()
			}

			// Check overflow before updating endLine so flush uses correct range
			newSize := bufBytes + len(line.Content)
			if len(buf) > 0 {
				newSize++
			}
			if newSize > detector.MaxEntryBytes() && len(buf) > 0 {
				flush()
			}

			if len(buf) == 0 {
				startLine = line.LineNumber
				bufBytes = len(line.Content)
			} else {
				bufBytes = newSize
			}
			endLine = line.LineNumber

			buf = append(buf, line.Content)
		}

		flush()
	}()
	return out
}

// MergeSlice merges a slice of log lines into logical entries.
// This is useful for non-streaming paths (analyze, debug commands).
// If no line is ever detected as a new entry, each line passes through individually.
func MergeSlice(lines []string, detector *Detector) []MergedLine {
	if len(lines) == 0 {
		return nil
	}

	var result []MergedLine
	var buf []string
	startLine := 0
	endLine := 0
	bufBytes := 0
	everDetected := false

	flush := func() {
		if len(buf) == 0 {
			return
		}
		result = append(result, MergedLine{
			StartLine: startLine,
			EndLine:   endLine,
			Content:   strings.Join(buf, "\n"),
		})
		buf = buf[:0]
		bufBytes = 0
	}

	for i, line := range lines {
		lineNum := i + 1
		isNew := detector.IsNewEntry(line)
		if isNew {
			everDetected = true
		}

		if !everDetected && len(buf) > 0 {
			flush()
		}

		if isNew && len(buf) > 0 {
			flush()
		}

		newSize := bufBytes + len(line)
		if len(buf) > 0 {
			newSize++
		}
		if newSize > detector.MaxEntryBytes() && len(buf) > 0 {
			flush()
		}

		if len(buf) == 0 {
			startLine = lineNum
			bufBytes = len(line)
		} else {
			bufBytes = newSize
		}
		endLine = lineNum

		buf = append(buf, line)
	}

	flush()
	return result
}
