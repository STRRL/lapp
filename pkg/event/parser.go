package event

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParsedLine is the normalized result of parsing a single raw log line.
type ParsedLine struct {
	SourceFormat string
	Event        Event
}

type lineParser interface {
	Parse(line string) (*ParsedLine, bool)
}

var defaultParsers = []lineParser{
	jsonLineParser{},
	logfmtLineParser{},
	keyValueLineParser{},
	prefixLineParser{},
	plainTextLineParser{},
}

var (
	timestampLayouts = []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}

	levelValues = map[string]string{
		"trace":   "trace",
		"debug":   "debug",
		"info":    "info",
		"warn":    "warn",
		"warning": "warn",
		"error":   "error",
		"err":     "error",
		"fatal":   "fatal",
	}

	timestampPrefixWithLevelPattern = regexp.MustCompile(`^\s*([0-9]{4}-[0-9]{2}-[0-9]{2}(?:[T ][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})?))\s+\[?([A-Za-z]+)\]?:?\b`)
	timestampPrefixPattern          = regexp.MustCompile(`^\s*([0-9]{4}-[0-9]{2}-[0-9]{2}(?:[T ][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})?))\b`)
	levelPrefixPattern              = regexp.MustCompile(`^\s*\[?([A-Za-z]+)\]?:?\b`)
)

// ParseLine runs the default parser chain and always returns a normalized event.
func ParseLine(line string) ParsedLine {
	for _, parser := range defaultParsers {
		if parsed, ok := parser.Parse(line); ok {
			return *parsed
		}
	}
	return ParsedLine{
		SourceFormat: SourceFormatPlainText,
		Event:        newBaseEvent(line),
	}
}

type jsonLineParser struct{}

func (jsonLineParser) Parse(line string) (*ParsedLine, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, false
	}

	event := newBaseEvent(line)
	for key, value := range payload {
		assignField(&event, key, stringifyValue(value))
	}

	return &ParsedLine{
		SourceFormat: SourceFormatJSON,
		Event:        event,
	}, true
}

type logfmtLineParser struct{}

func (logfmtLineParser) Parse(line string) (*ParsedLine, bool) {
	assignments, sawQuotedValue, ok := scanAssignments(line, true)
	if !ok || !sawQuotedValue || len(assignments) == 0 {
		return nil, false
	}
	return buildParsedLine(SourceFormatLogfmt, line, assignments), true
}

type keyValueLineParser struct{}

func (keyValueLineParser) Parse(line string) (*ParsedLine, bool) {
	assignments, sawQuotedValue, ok := scanAssignments(line, false)
	if !ok || sawQuotedValue || len(assignments) == 0 {
		return nil, false
	}
	return buildParsedLine(SourceFormatKeyValue, line, assignments), true
}

type prefixLineParser struct{}

func (prefixLineParser) Parse(line string) (*ParsedLine, bool) {
	event := newBaseEvent(line)
	matched := false

	if match := timestampPrefixWithLevelPattern.FindStringSubmatch(line); len(match) == 3 {
		if ts, ok := parseTimestamp(match[1]); ok {
			event.Timestamp = &ts
			matched = true
		}
		if level, ok := canonicalizeLevel(match[2]); ok {
			event.Attrs["level"] = level
			matched = true
		}
	}

	if !matched {
		if match := timestampPrefixPattern.FindStringSubmatch(line); len(match) == 2 {
			if ts, ok := parseTimestamp(match[1]); ok {
				event.Timestamp = &ts
				matched = true
			}
		}
	}

	if !matched {
		if match := levelPrefixPattern.FindStringSubmatch(line); len(match) == 2 {
			if level, ok := canonicalizeLevel(match[1]); ok {
				event.Attrs["level"] = level
				matched = true
			}
		}
	}

	if !matched {
		return nil, false
	}

	return &ParsedLine{
		SourceFormat: SourceFormatPlainText,
		Event:        event,
	}, true
}

type plainTextLineParser struct{}

func (plainTextLineParser) Parse(line string) (*ParsedLine, bool) {
	return &ParsedLine{
		SourceFormat: SourceFormatPlainText,
		Event:        newBaseEvent(line),
	}, true
}

type assignment struct {
	key   string
	value string
}

type assignmentScanResult struct {
	next   int
	item   assignment
	quoted bool
	ok     bool
	done   bool
}

type assignmentKeyResult struct {
	key  string
	next int
	ok   bool
}

type assignmentValueResult struct {
	value  string
	next   int
	quoted bool
	ok     bool
}

func buildParsedLine(sourceFormat, line string, assignments []assignment) *ParsedLine {
	event := newBaseEvent(line)
	for _, item := range assignments {
		assignField(&event, item.key, item.value)
	}
	return &ParsedLine{
		SourceFormat: sourceFormat,
		Event:        event,
	}
}

func newBaseEvent(line string) Event {
	return Event{
		Text:     line,
		Attrs:    make(map[string]string),
		Inferred: &Inferred{},
	}
}

func assignField(event *Event, key, value string) {
	normalizedKey := strings.TrimSpace(strings.ToLower(key))
	if normalizedKey == "" || value == "" {
		return
	}

	if isTimestampKey(normalizedKey) {
		if ts, ok := parseTimestamp(value); ok && event.Timestamp == nil {
			event.Timestamp = &ts
		}
		return
	}

	switch normalizedKey {
	case "level", "severity", "lvl", "log.level":
		if level, ok := canonicalizeLevel(value); ok {
			event.Attrs["level"] = level
			return
		}
	case "service", "service_name", "service.name":
		event.Attrs["service"] = strings.TrimSpace(value)
		return
	case "env", "environment":
		event.Attrs["env"] = strings.ToLower(strings.TrimSpace(value))
		return
	}

	event.Attrs[strings.TrimSpace(key)] = strings.TrimSpace(value)
}

func isTimestampKey(key string) bool {
	switch key {
	case "ts", "timestamp", "time", "@timestamp":
		return true
	default:
		return false
	}
}

func parseTimestamp(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	for _, layout := range timestampLayouts {
		ts, err := time.Parse(layout, value)
		if err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func canonicalizeLevel(raw string) (string, bool) {
	level := strings.ToLower(strings.Trim(strings.TrimSpace(raw), "[]"))
	canonical, ok := levelValues[level]
	return canonical, ok
}

func stringifyValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case []any, map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return fmt.Sprint(v)
	}
}

func scanAssignments(line string, allowQuotedValues bool) ([]assignment, bool, bool) {
	var (
		assignments    []assignment
		sawQuotedValue bool
	)

	for i := 0; i < len(line); {
		result := scanAssignment(line, i, allowQuotedValues)
		if !result.ok {
			return nil, false, false
		}
		if result.done {
			break
		}
		assignments = append(assignments, result.item)
		sawQuotedValue = sawQuotedValue || result.quoted
		i = result.next
	}

	if len(assignments) == 0 {
		return nil, false, false
	}
	return assignments, sawQuotedValue, true
}

func scanAssignment(line string, start int, allowQuotedValues bool) assignmentScanResult {
	i := skipSpaces(line, start)
	if i >= len(line) {
		return assignmentScanResult{done: true, ok: true}
	}

	keyResult := scanAssignmentKey(line, i)
	if !keyResult.ok {
		return assignmentScanResult{}
	}

	valueResult := scanAssignmentValue(line, keyResult.next, allowQuotedValues)
	if !valueResult.ok {
		return assignmentScanResult{}
	}

	return assignmentScanResult{
		next:   valueResult.next,
		item:   assignment{key: keyResult.key, value: valueResult.value},
		quoted: valueResult.quoted,
		ok:     true,
	}
}

func skipSpaces(line string, start int) int {
	i := start
	for i < len(line) && line[i] == ' ' {
		i++
	}
	return i
}

func scanAssignmentKey(line string, start int) assignmentKeyResult {
	i := start
	for i < len(line) && line[i] != '=' && line[i] != ' ' {
		i++
	}
	if i >= len(line) || line[i] != '=' {
		return assignmentKeyResult{}
	}

	key := strings.TrimSpace(line[start:i])
	if key == "" {
		return assignmentKeyResult{}
	}

	return assignmentKeyResult{key: key, next: i + 1, ok: true}
}

func scanAssignmentValue(line string, start int, allowQuotedValues bool) assignmentValueResult {
	if start >= len(line) {
		return assignmentValueResult{next: start, ok: true}
	}

	if line[start] == '"' {
		if !allowQuotedValues {
			return assignmentValueResult{}
		}
		result := scanQuotedValue(line, start+1)
		if !result.ok {
			return assignmentValueResult{}
		}
		return assignmentValueResult{value: result.value, next: result.next, quoted: true, ok: true}
	}

	result := scanUnquotedValue(line, start)
	return assignmentValueResult{value: result.value, next: result.next, ok: true}
}

func scanQuotedValue(line string, start int) assignmentValueResult {
	i := start
	var valueBuilder strings.Builder
	for i < len(line) {
		if line[i] == '\\' && i+1 < len(line) {
			valueBuilder.WriteByte(line[i+1])
			i += 2
			continue
		}
		if line[i] == '"' {
			return assignmentValueResult{value: valueBuilder.String(), next: i + 1, ok: true}
		}
		valueBuilder.WriteByte(line[i])
		i++
	}
	return assignmentValueResult{}
}

func scanUnquotedValue(line string, start int) assignmentValueResult {
	i := start
	for i < len(line) && line[i] != ' ' {
		i++
	}
	return assignmentValueResult{value: line[start:i], next: i}
}
