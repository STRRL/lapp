package ndjson

import (
	"bytes"
	"encoding/json"
	"strings"
)

// messageFields are checked in order; the first string value wins.
var messageFields = []string{"message", "msg", "log", "error"}

// severityFields are checked in order; the first string value wins.
// Numeric levels are ignored in v1.
var severityFields = []string{"severity", "level"}

// Extract converts one NDJSON line into the text line fed to pattern mining.
// Envelope entries {"ts": ..., "severity": ..., "payload": {...}} extract
// from the payload; any other object is the payload itself. The extracted line is
// "<severity> <message>", just "<message>" when no severity-like string field
// exists, or the compact payload JSON when no message-like string field
// exists. A line that does not parse as a JSON object is returned unchanged.
func Extract(line string) string {
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry == nil {
		return line
	}

	payload, severity := splitEnvelope(entry)
	if severity == "" {
		severity = firstStringField(payload, severityFields)
	}

	message := firstStringField(payload, messageFields)
	if message == "" {
		return compactJSON(payload)
	}
	if severity == "" {
		return message
	}
	return severity + " " + message
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

func compactJSON(payload map[string]any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
