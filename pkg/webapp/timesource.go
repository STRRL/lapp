package webapp

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"regexp"
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

// applyTimeSource fills missing line times from the service's chosen time
// column and reports the time facts. A column is only applied when no line
// has a time of its own, so automatic detection always wins.
func applyTimeSource(lines []workspace.TaggedLine, column string) demoTimeSource {
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
	var valueOf func(content string) (string, bool)
	switch {
	case len(sample) > 0 && ndjson.DetectFormat(sample) == ndjson.FormatNDJSON:
		src.Format = timeFormatJSON
		src.Fields = ndjson.TimestampFields()
		src.Columns = jsonFieldNames(sample)
		valueOf = jsonFieldValue(column)
	default:
		names, header := csvColumns(sample)
		if names == nil {
			return src
		}
		src.Format = timeFormatCSV
		src.Columns = names
		valueOf = csvColumnValue(names, header, column)
	}
	if src.Timed > 0 {
		src.Columns = nil
		return src
	}
	if column == "" || !contains(src.Columns, column) {
		return src
	}
	src.Column = column
	for i := range lines {
		value, ok := valueOf(lines[i].Content)
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

func jsonFieldValue(field string) func(string) (string, bool) {
	return func(content string) (string, bool) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(content), &entry); err != nil {
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

// csvColumns treats the sample as CSV when every line splits into the same
// number of fields (at least three). The first line is a header when all of
// its fields look like names; otherwise columns are numbered.
func csvColumns(sample []string) (names []string, header bool) {
	width := 0
	var first []string
	rows := 0
	for _, line := range sample {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields, err := csvFields(line)
		if err != nil || len(fields) < 3 || (width > 0 && len(fields) != width) {
			return nil, false
		}
		if width == 0 {
			width, first = len(fields), fields
		}
		rows++
	}
	if rows < 2 {
		return nil, false
	}
	header = true
	for _, f := range first {
		if !csvHeaderName.MatchString(strings.TrimSpace(f)) {
			header = false
			break
		}
	}
	names = make([]string, width)
	for i := range names {
		if header {
			names[i] = strings.TrimSpace(first[i])
		} else {
			names[i] = "Column " + strconv.Itoa(i+1)
		}
	}
	return names, header
}

func csvFields(line string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	return r.Read()
}

func csvColumnValue(names []string, header bool, column string) func(string) (string, bool) {
	idx := -1
	for i, n := range names {
		if n == column {
			idx = i
		}
	}
	return func(content string) (string, bool) {
		fields, err := csvFields(content)
		if err != nil || idx < 0 || idx >= len(fields) {
			return "", false
		}
		if header && strings.TrimSpace(fields[idx]) == names[idx] {
			return "", false
		}
		return fields[idx], true
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
