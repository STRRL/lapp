package ndjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// grafanaLevelFields are the Grafana Explore export fields checked in order
// for the entry level; the first non-empty value other than "unknown" wins.
var grafanaLevelFields = []string{"detected_level", "severity_text", "level"}

// grafanaHeaderPeekSize bounds how much of a file is inspected to find the
// CSV header row.
const grafanaHeaderPeekSize = 64 * 1024

// grafanaTextSummaryLine matches the first line of a Grafana Explore text
// export, e.g. `: "1000 lines shown — 53.63% (16min 5sec) of 30min"`.
var grafanaTextSummaryLine = regexp.MustCompile(`^[:\s"]*\d+ lines shown\b`)

// grafanaJSONItem is one entry of a Grafana Explore "Download > JSON" logs
// export: the log text, a Unix-nanosecond timestamp string, an ISO 8601 date,
// and the flat labels/fields map.
type grafanaJSONItem struct {
	Line      *string        `json:"line"`
	Timestamp string         `json:"timestamp"`
	Date      string         `json:"date"`
	Fields    map[string]any `json:"fields"`
}

// grafanaEntry is the NDJSON line a Grafana export entry is stored as. It
// uses the VictoriaLogs field names Extract already reads (_time, _msg) plus
// level; the export fields stay nested so they cannot collide with them.
type grafanaEntry struct {
	Time   string         `json:"_time,omitempty"`
	Level  string         `json:"level,omitempty"`
	Msg    string         `json:"_msg"`
	Fields map[string]any `json:"fields,omitempty"`
	at     time.Time
}

// Normalize returns the content to store for one incoming log file. A
// Grafana Explore logs export is converted to NDJSON, one entry per line in
// chronological order; any other content is returned unchanged. Two export
// shapes are recognized:
//
//   - JSON: one top-level array whose items carry a string "line" and a
//     "timestamp" (Unix nanoseconds) or "date" (ISO 8601);
//   - CSV: a header row with "Date" and "Line" columns, read per RFC 4180 so
//     quoted multi-line bodies stay one entry.
//
// A Grafana text export stays text; only its summary lines ("<n> lines
// shown", "Common labels: ...", and the blank lines after them) are dropped,
// since they are not log entries. Only content that looks like a JSON or CSV export is buffered;
// everything else streams through.
func Normalize(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, grafanaHeaderPeekSize)
	head, err := br.Peek(grafanaHeaderPeekSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, errors.Errorf("read log: %w", err)
	}
	if size := grafanaTextPreambleSize(head); size > 0 {
		if _, err := br.Discard(size); err != nil {
			return nil, errors.Errorf("read log: %w", err)
		}
		return br, nil
	}
	convert := grafanaConverter(head)
	if convert == nil {
		return br, nil
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return nil, errors.Errorf("read log: %w", err)
	}
	entries, ok := convert(data)
	if !ok || len(entries) == 0 {
		return bytes.NewReader(data), nil
	}
	sortChronologically(entries)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return nil, errors.Errorf("encode log entry: %w", err)
		}
	}
	return &buf, nil
}

// grafanaTextPreambleSize returns the byte length of the summary lines a
// Grafana Explore text export opens with, or 0 when head has none.
func grafanaTextPreambleSize(head []byte) int {
	first, rest, ok := bytes.Cut(head, []byte("\n"))
	if !ok || !grafanaTextSummaryLine.Match(first) {
		return 0
	}
	size := len(first) + 1
	if second, _, ok := bytes.Cut(rest, []byte("\n")); ok && bytes.HasPrefix(second, []byte("Common labels:")) {
		size += len(second) + 1
	}
	// The blank lines between the summary and the first entry go too.
	for {
		line, _, ok := bytes.Cut(head[size:], []byte("\n"))
		if !ok || len(bytes.TrimSpace(line)) > 0 {
			return size
		}
		size += len(line) + 1
	}
}

// grafanaConverter picks the export parser from the start of a file, or
// returns nil when the file cannot be a Grafana export.
func grafanaConverter(head []byte) func([]byte) ([]grafanaEntry, bool) {
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("[")) {
		return parseGrafanaJSON
	}
	headerLine, _, _ := bytes.Cut(head, []byte("\n"))
	headerLine = bytes.TrimSuffix(headerLine, []byte("\r"))
	header, _, err := ReadCSV(bytes.NewReader(headerLine))
	if err != nil {
		return nil
	}
	if _, _, ok := grafanaCSVColumns(header); ok {
		return parseGrafanaCSV
	}
	return nil
}

// parseGrafanaJSON decodes a JSON export with a streaming decoder. ok is
// false when data is not such an export.
func parseGrafanaJSON(data []byte) ([]grafanaEntry, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, false
	}
	var entries []grafanaEntry
	for decoder.More() {
		var item grafanaJSONItem
		if err := decoder.Decode(&item); err != nil {
			return nil, false
		}
		if item.Line == nil || (item.Timestamp == "" && item.Date == "") {
			return nil, false
		}
		at := parseUnixNanos(item.Timestamp)
		if at.IsZero() {
			at = parseISODate(item.Date)
		}
		entries = append(entries, newGrafanaEntry(*item.Line, at, item.Fields))
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return entries, true
}

// parseGrafanaCSV reads a CSV export. Columns other than Date, Time, and
// Line become the entry fields; empty cells are dropped. ok is false when
// data is not such an export.
func parseGrafanaCSV(data []byte) ([]grafanaEntry, bool) {
	header, records, err := ReadCSV(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	dateCol, lineCol, ok := grafanaCSVColumns(header)
	if !ok {
		return nil, false
	}
	timeCol := csvColumn(header, "Time")
	entries := make([]grafanaEntry, 0, len(records))
	for _, record := range records {
		if len(record) <= lineCol || len(record) <= dateCol {
			return nil, false
		}
		entries = append(entries, grafanaCSVEntry(header, record, dateCol, timeCol, lineCol))
	}
	return entries, true
}

func grafanaCSVEntry(header, record []string, dateCol, timeCol, lineCol int) grafanaEntry {
	at := parseISODate(record[dateCol])
	if at.IsZero() && timeCol >= 0 && timeCol < len(record) {
		at = parseUnixMillis(record[timeCol])
	}
	fields := map[string]any{}
	for i, value := range record {
		if i == dateCol || i == timeCol || i == lineCol || i >= len(header) || value == "" {
			continue
		}
		fields[header[i]] = value
	}
	return newGrafanaEntry(record[lineCol], at, fields)
}

// grafanaCSVColumns returns the Date and Line column indexes of a CSV
// export header.
func grafanaCSVColumns(header []string) (dateCol, lineCol int, ok bool) {
	dateCol, lineCol = csvColumn(header, "Date"), csvColumn(header, "Line")
	return dateCol, lineCol, dateCol >= 0 && lineCol >= 0
}

func csvColumn(header []string, name string) int {
	for i, column := range header {
		if strings.EqualFold(strings.TrimSpace(column), name) {
			return i
		}
	}
	return -1
}

func newGrafanaEntry(line string, at time.Time, fields map[string]any) grafanaEntry {
	entry := grafanaEntry{Msg: line, Fields: fields, at: at}
	if !at.IsZero() {
		entry.Time = at.Format(time.RFC3339Nano)
	}
	for _, field := range grafanaLevelFields {
		if level, ok := fields[field].(string); ok && level != "" && level != "unknown" {
			entry.Level = level
			break
		}
	}
	return entry
}

// sortChronologically orders entries oldest first. Grafana exports newest
// first by default, so a newest-first export is reversed before the stable
// sort; entries with equal timestamps then end up oldest first too.
func sortChronologically(entries []grafanaEntry) {
	if entries[0].at.After(entries[len(entries)-1].at) {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
}

func parseUnixNanos(value string) time.Time {
	ns, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func parseUnixMillis(value string) time.Time {
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func parseISODate(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
