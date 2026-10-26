package ndjson

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// messageFields are checked in order; the first string value wins. "_msg" is
// the VictoriaLogs message field; it comes first because VictoriaLogs query
// results may also carry user fields such as "error" next to it.
var messageFields = []string{"_msg", "message", "msg", "log", "error"}

// timestampFields are checked in order on the top-level entry; the first
// RFC 3339 string wins. "_time" is the VictoriaLogs record time and comes
// first so a user field named "ts" cannot override it; "ts" is the GCP import
// envelope timestamp (ADR 0006), which has no "_time".
var timestampFields = []string{"_time", "ts"}

// TimestampFields lists the top-level fields a time is read from, in order,
// so the UI can name them when none is present.
func TimestampFields() []string {
	return append([]string(nil), timestampFields...)
}

// severityFields are checked in order; the first string value wins.
// Numeric levels are ignored in v1.
var severityFields = []string{"severity", "level"}

// Extract converts one NDJSON line into the text line fed to pattern mining.
// Envelope entries {"ts": ..., "severity": ..., "payload": {...}} extract
// from the payload; any other object is the payload itself. The extracted line is
// the message, or the compact payload JSON when no message-like string field
// exists. A line that does not parse as a JSON object is returned unchanged.
// The severity and timestamp are kept out of the extracted line, so the same
// log mines to the same pattern whatever format it was exported in; they are
// returned separately ("" and zero when absent).
func Extract(line string) (text, severity string, timestamp time.Time) {
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry == nil {
		return line, "", time.Time{}
	}
	timestamp = firstTimestampField(entry)

	payload, severity := splitEnvelope(entry)
	if severity == "" {
		severity = firstStringField(payload, severityFields)
	}

	message := firstStringField(payload, messageFields)
	if message == "" {
		return compactJSON(payload), severity, timestamp
	}
	return message, severity, timestamp
}

// splitEnvelope returns the payload object and the envelope severity when the
// entry matches the fixed envelope shape, otherwise the entry itself. The
// envelope is the importer's fixed contract (ADR 0006): ts, a string severity,
// and a payload object must all be present; anything less is an arbitrary
// user shape and is extracted as a whole.
func splitEnvelope(entry map[string]any) (payload map[string]any, severity string) {
	nested, ok := entry["payload"].(map[string]any)
	if !ok {
		return entry, ""
	}
	if _, hasTS := entry["ts"]; !hasTS {
		return entry, ""
	}
	envelopeSeverity, ok := entry["severity"].(string)
	if !ok || envelopeSeverity == "" {
		return entry, ""
	}
	return nested, envelopeSeverity
}

func firstStringField(obj map[string]any, fields []string) string {
	for _, field := range fields {
		if value, ok := obj[field].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstTimestampField(obj map[string]any) time.Time {
	for _, field := range timestampFields {
		value, ok := obj[field].(string)
		if !ok {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func compactJSON(payload map[string]any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
