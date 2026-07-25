// Package gcplog pulls log entries from GCP Cloud Logging and converts them
// into the workspace NDJSON envelope defined by ADR 0006.
package gcplog

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// DefaultSeverity is GCP's severity for entries without an explicit level.
const DefaultSeverity = "DEFAULT"

// Entry is one GCP Cloud Logging entry, using the REST JSON field names.
// At most one of JSONPayload and TextPayload is set. TextPayload is a
// pointer so a present but empty text payload stays distinguishable from an
// absent one.
type Entry struct {
	Timestamp   time.Time       `json:"timestamp"`
	Severity    string          `json:"severity,omitempty"`
	JSONPayload json.RawMessage `json:"jsonPayload,omitempty"`
	TextPayload *string         `json:"textPayload,omitempty"`
}

// envelope is the fixed NDJSON shape imported entries land in (ADR 0006).
type envelope struct {
	TS       string          `json:"ts"`
	Severity string          `json:"severity"`
	Payload  json.RawMessage `json:"payload"`
}

// ConvertEntry converts one GCP log entry into one envelope NDJSON line
// without a trailing newline. The timestamp is normalized to RFC3339 UTC,
// a missing severity becomes DEFAULT, jsonPayload is kept as is (compacted
// to a single line), and textPayload becomes {"message": <text>}. Provider
// metadata such as labels, trace, and resource is intentionally dropped
// here; it belongs in the ImportRun record, not in the log lines.
func ConvertEntry(entry Entry) (string, error) {
	payload, err := entryPayload(entry)
	if err != nil {
		return "", err
	}
	severity := entry.Severity
	if severity == "" {
		severity = DefaultSeverity
	}
	return marshalEnvelopeLine(envelope{
		TS:       entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Severity: severity,
		Payload:  payload,
	})
}

func entryPayload(entry Entry) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(entry.JSONPayload)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var buf bytes.Buffer
		if err := json.Compact(&buf, trimmed); err != nil {
			return nil, errors.Errorf("compact jsonPayload: %w", err)
		}
		return buf.Bytes(), nil
	}
	if entry.TextPayload != nil {
		data, err := json.Marshal(map[string]string{"message": *entry.TextPayload})
		if err != nil {
			return nil, errors.Errorf("marshal textPayload: %w", err)
		}
		return data, nil
	}
	return json.RawMessage("{}"), nil
}

func marshalEnvelopeLine(env envelope) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(env); err != nil {
		return "", errors.Errorf("marshal envelope: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
