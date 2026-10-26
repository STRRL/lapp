package logsource

import (
	"bufio"
	"io"

	"github.com/go-errors/errors"
)

// MaxLineBytes is the longest line lapp keeps. Longer lines are cut at this
// size and the rest of the line is dropped, so one oversized line never
// fails a whole read.
const MaxLineBytes = 16 << 20

// ScanLines calls fn for each line in r, without its trailing "\n" or
// "\r\n", like bufio.ScanLines. truncated reports that the line was cut at
// MaxLineBytes. A non-nil error from fn stops the scan and is returned.
func ScanLines(r io.Reader, fn func(line string, truncated bool) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	truncated := false
	for {
		chunk, err := br.ReadSlice('\n')
		complete := err == nil
		if complete {
			chunk = chunk[:len(chunk)-1]
		}
		room := MaxLineBytes - len(buf)
		if len(chunk) > room {
			chunk, truncated = chunk[:room], true
		}
		buf = append(buf, chunk...)

		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case complete || (errors.Is(err, io.EOF) && (len(buf) > 0 || truncated)):
			line := buf
			if !truncated && len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if ferr := fn(string(line), truncated); ferr != nil {
				return ferr
			}
			if cap(buf) > 1<<20 {
				// Don't hold on to the memory of one huge line.
				buf = nil
			}
			buf, truncated = buf[:0], false
			if !complete {
				return nil
			}
		case errors.Is(err, io.EOF):
			return nil
		default:
			return errors.Errorf("read line: %w", err)
		}
	}
}
