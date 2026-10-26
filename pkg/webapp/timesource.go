package webapp

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/strrl/lapp/pkg/logheader"
	"github.com/strrl/lapp/pkg/ndjson"
	"github.com/strrl/lapp/pkg/workspace"
)

const (
	// Below this share of entries (after merging multi-line entries) with a
	// time, a volume chart would misstate the file, so the overview explains
	// the gap instead. Between this and 100% the chart says how many are timed.
	minTimedShare = 0.5
	minTimedLines = 2
	// Lines sampled to classify a file and list its columns or fields.
	timeSourceSample = 200
)

// Time source formats.
const (
	timeFormatText = "text"
	timeFormatJSON = "json"
	timeFormatCSV  = "csv"
)

// demoTimeSource states where the line times came from, so the frontend can
// say why there is no volume chart and offer a time column when lapp could
// not find one on its own.
type demoTimeSource struct {
	Format string `json:"format"` // text, json, csv
	Timed  int    `json:"timed"`  // lines with a time
	Lines  int    `json:"lines"`
	// Fields are the JSON fields lapp reads a time from automatically.
	Fields []string `json:"fields,omitempty"`
	// Columns are the CSV columns or JSON fields the user can pick as the
	// time; only set for CSV and JSON files without a recognized time.
	Columns []string `json:"columns,omitempty"`
	// Column is the service's chosen time column, if any.
	Column           string `json:"column,omitempty"`
	ColumnUnreadable bool   `json:"columnUnreadable,omitempty"`
}

var csvHeaderName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_ .-]*$`)

// maxCSVHeaderName is the longest first-row field still taken for a column
// name.
const maxCSVHeaderName = 64

// applyTimeSource fills missing line times from the service's chosen time
// column and reports the time facts. A column is only applied when no line
// has a time of its own, so automatic detection always wins. dir is the
// workspace directory; CSV files are read from its logs.
func applyTimeSource(dir string, lines []workspace.TaggedLine, column string) demoTimeSource {
	src := demoTimeSource{Format: timeFormatText, Lines: len(lines)}
	for _, tl := range lines {
		if tl.Timestamp != nil {
			src.Timed++
		}
	}
	sample := make([]string, 0, timeSourceSample)
	for _, tl := range lines {
		if len(sample) == timeSourceSample {
			break
		}
		sample = append(sample, tl.Content)
	}
	isJSON := len(sample) > 0 && ndjson.DetectFormat(sample) == ndjson.FormatNDJSON
	if isJSON {
		src.Format = timeFormatJSON
		src.Fields = ndjson.TimestampFields()
	}
	if src.Timed > 0 {
		return src
	}
	var valueOf func(tl workspace.TaggedLine) (string, bool)
	if isJSON {
		src.Columns = jsonFieldNames(sample)
		valueOf = jsonFieldValue(column)
	} else {
		logs := readCSVLogs(dir)
		if logs == nil {
			return src
		}
		src.Format = timeFormatCSV
		src.Columns = logs.names
		valueOf = logs.value(column)
	}
	if column == "" || !contains(src.Columns, column) {
		return src
	}
	src.Column = column
	for i := range lines {
		value, ok := valueOf(lines[i])
		if !ok {
			continue
		}
		if t := parseTimeValue(value); !t.IsZero() {
			lines[i].Timestamp = &t
			src.Timed++
		}
	}
	src.ColumnUnreadable = src.Timed == 0
	return src
}

// enoughTimes reports whether the timed lines can draw a volume chart.
func (s demoTimeSource) enoughTimes() bool {
	return s.Timed >= minTimedLines && float64(s.Timed) >= minTimedShare*float64(s.Lines)
}

// jsonFieldNames lists top-level fields holding strings or numbers, in order
// of first appearance.
func jsonFieldNames(sample []string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range sample {
		dec := json.NewDecoder(strings.NewReader(line))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			continue
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				break
			}
			var value any
			if err := dec.Decode(&value); err != nil {
				break
			}
			name, _ := key.(string)
			switch value.(type) {
			case string, float64:
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func jsonFieldValue(field string) func(workspace.TaggedLine) (string, bool) {
	return func(tl workspace.TaggedLine) (string, bool) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(tl.Content), &entry); err != nil {
			return "", false
		}
		switch v := entry[field].(type) {
		case string:
			return v, true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), true
		}
		return "", false
	}
}

// csvLogs are a workspace's log files read as CSV with one shared set of
// column names.
type csvLogs struct {
	names []string
	files map[string]csvFile
}

// csvFile is one CSV log file: its data records and, per physical line
// (1-based), the record that line belongs to. A quoted field can span lines,
// so every line of a record maps to it; header lines map to none.
type csvFile struct {
	rows     [][]string
	recordAt map[int]int
}

// readCSVLogs reads every log file in the workspace with ndjson.ReadCSV and
// returns nil unless all of them are CSV with the same columns.
func readCSVLogs(dir string) *csvLogs {
	names, err := workspace.ListLogFiles(dir)
	if err != nil || len(names) == 0 {
		return nil
	}
	logs := &csvLogs{files: map[string]csvFile{}}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, "logs", name))
		if err != nil {
			return nil
		}
		columns, file, ok := parseCSVLog(data)
		if !ok || (logs.names != nil && !slices.Equal(columns, logs.names)) {
			return nil
		}
		logs.names = columns
		logs.files[name] = file
	}
	return logs
}

// parseCSVLog treats data as CSV when it has at least two columns and every
// record has as many fields as the first row. The first row is a header when
// all of its fields look like names; otherwise columns are numbered, which
// needs at least three of them so ordinary text with one comma per line is
// not taken for CSV.
func parseCSVLog(data []byte) ([]string, csvFile, bool) {
	first, records, err := ndjson.ReadCSV(bytes.NewReader(data))
	if err != nil || len(first) < 2 || len(records) == 0 {
		return nil, csvFile{}, false
	}
	for _, record := range records {
		if len(record) != len(first) {
			return nil, csvFile{}, false
		}
	}
	header := true
	for _, f := range first {
		if f = strings.TrimSpace(f); len(f) > maxCSVHeaderName || !csvHeaderName.MatchString(f) {
			header = false
			break
		}
	}
	if !header && len(first) < 3 {
		return nil, csvFile{}, false
	}
	names := make([]string, len(first))
	for i := range names {
		if header {
			names[i] = strings.TrimSpace(first[i])
		} else {
			names[i] = "Column " + strconv.Itoa(i+1)
		}
	}
	all := append([][]string{first}, records...)
	file := csvFile{rows: all, recordAt: map[int]int{}}
	if header {
		file.rows = records
	}
	// Walk the physical lines the way the CSV reader did: it skips empty
	// lines between records, and a record spans one line plus one per
	// newline inside its quoted fields.
	physical := strings.Split(string(data), "\n")
	line := 0
	for i, record := range all {
		for line < len(physical) && strings.TrimRight(physical[line], "\r") == "" {
			line++
		}
		span := 1
		for _, f := range record {
			span += strings.Count(f, "\n")
		}
		row := i
		if header {
			row--
		}
		if row >= 0 {
			for k := 0; k < span; k++ {
				file.recordAt[line+k+1] = row
			}
		}
		line += span
	}
	return names, file, true
}

// value returns a function reading the named column of the record an entry
// starts in.
func (l *csvLogs) value(column string) func(workspace.TaggedLine) (string, bool) {
	idx := slices.Index(l.names, column)
	return func(tl workspace.TaggedLine) (string, bool) {
		file, ok := l.files[tl.FileName]
		if !ok || idx < 0 {
			return "", false
		}
		row, ok := file.recordAt[tl.LineNum]
		if !ok {
			return "", false
		}
		return file.rows[row][idx], true
	}
}

// parseTimeValue reads a picked column value: any header date-time layout,
// or Unix seconds / milliseconds.
func parseTimeValue(value string) time.Time {
	if t := logheader.ParseTime(value); !t.IsZero() {
		return t
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	switch {
	case n >= 1e11 && n < 1e14: // milliseconds
		return time.UnixMilli(int64(n)).UTC()
	case n >= 1e8 && n < 1e11: // seconds
		sec, frac := math.Modf(n)
		return time.Unix(int64(sec), int64(frac*1e9)).UTC()
	}
	return time.Time{}
}
