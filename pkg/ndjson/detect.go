// Package ndjson classifies log files as NDJSON and projects structured
// entries to text lines for pattern mining, per ADR 0006.
package ndjson

import (
	"encoding/json"
	"strings"
)

// Format classifies the on-disk format of a log file.
type Format string

const (
	// FormatText marks a file for the plain text pipeline.
	FormatText Format = "text"
	// FormatNDJSON marks a file whose lines are JSON objects, one per line.
	FormatNDJSON Format = "ndjson"
)

// DetectFormat classifies a file's lines. A file is NDJSON only when it has
// at least one non-empty line and every non-empty line parses as a JSON
// object. Files mixing text with JSON lines stay on the text path, so the
// projection never has to guess on a half-structured file.
func DetectFormat(lines []string) Format {
	sampled := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !isJSONObject(trimmed) {
			return FormatText
		}
		sampled++
	}
	if sampled == 0 {
		return FormatText
	}
	return FormatNDJSON
}

func isJSONObject(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj map[string]any
	return json.Unmarshal([]byte(trimmed), &obj) == nil
}
