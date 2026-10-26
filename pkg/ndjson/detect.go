// Package ndjson classifies log files as NDJSON and extracts the text lines
// fed to pattern mining from structured entries, per ADR 0006.
package ndjson

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
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

// isBinary reports whether the start of the file has NUL bytes and is not
// valid UTF-8. NUL padding alone (left by copytruncate) and Latin-1 text
// alone stay readable.
func isBinary(lines []string) bool {
	var head []byte
	for _, line := range lines {
		head = append(head, line...)
		head = append(head, '\n')
		if len(head) >= binarySniffBytes {
			head = head[:binarySniffBytes]
			break
		}
	}
	if !bytes.ContainsRune(head, 0) {
		return false
	}
	// Ignore a rune cut at the end of the sample.
	for i := 0; i < utf8.UTFMax && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	return !utf8.Valid(head)
}
