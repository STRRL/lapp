// Package ndjson classifies log files as NDJSON, normalizes Grafana Explore
// exports to NDJSON when they land, and extracts the text lines fed to
// pattern mining from structured entries, per ADR 0006.
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
	// FormatUnrecognized marks a file lapp can't read as log lines: one JSON
	// document spanning the whole file (e.g. a JSON array export) or binary.
	FormatUnrecognized Format = "unrecognized"
)

// binarySniffBytes is how much of a file is checked for binary content.
const binarySniffBytes = 8000

// DetectFormat classifies a file's lines. A file is NDJSON only when it has
// at least one non-empty line and every non-empty line parses as a JSON
// object. Files mixing text with JSON lines stay on the text path, so the
// extraction never has to guess on a half-structured file. A file that is
// one JSON document rather than one object per line, or that looks binary,
// is unrecognized.
func DetectFormat(lines []string) Format {
	if isBinary(lines) {
		return FormatUnrecognized
	}
	sampled := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !isJSONObject(trimmed) {
			if isJSONDocument(lines) {
				return FormatUnrecognized
			}
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

// isJSONDocument reports whether the non-empty content of the file is a
// single JSON array or object, such as a pretty-printed export.
func isJSONDocument(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "{") {
			return false
		}
		break
	}
	return json.Valid([]byte(strings.Join(lines, "\n")))
}

// isBinary reports whether the start of the file looks binary: more than
// one in ten bytes are NUL or other control bytes. Leading NUL runs (the
// padding copytruncate leaves), a stray NUL and non-UTF-8 text such as
// Latin-1 stay readable.
func isBinary(lines []string) bool {
	sampled, control := 0, 0
	for _, line := range lines {
		line = strings.TrimLeft(line, "\x00")
		for i := 0; i < len(line) && sampled < binarySniffBytes; i++ {
			sampled++
			switch c := line[i]; {
			case c == '\t' || c == '\r' || c == '\f' || c == '\v' || c == 0x1b:
				// Whitespace and ANSI color escapes are text.
			case c < 0x20 || c == 0x7f:
				control++
			}
		}
		if sampled >= binarySniffBytes {
			break
		}
	}
	return sampled > 0 && control*10 > sampled
}
