package ndjson

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"io"

	"github.com/go-errors/errors"
)

// utf8BOM is the byte order mark some tools write before CSV content.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadCSV reads CSV log content per RFC 4180: a quoted field may contain
// separators, quotes, and newlines (LF or CRLF), so one record can span
// several physical lines. Stray quotes are read leniently and records may
// have different field counts. A leading UTF-8 BOM is dropped before
// parsing, so a quoted first header cell still parses. It returns the header
// row and the records after it; err is set when the content is not CSV.
// This is the single CSV parsing path for log files.
func ReadCSV(r io.Reader) (header []string, records [][]string, err error) {
	br := bufio.NewReader(r)
	if head, _ := br.Peek(len(utf8BOM)); bytes.Equal(head, utf8BOM) {
		if _, err := br.Discard(len(utf8BOM)); err != nil {
			return nil, nil, errors.Errorf("read csv: %w", err)
		}
	}
	reader := csv.NewReader(br)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	header, err = reader.Read()
	if err != nil {
		return nil, nil, errors.Errorf("read csv header: %w", err)
	}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return header, records, nil
		}
		if err != nil {
			return nil, nil, errors.Errorf("read csv record: %w", err)
		}
		records = append(records, record)
	}
}
