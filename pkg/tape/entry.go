// Package tape implements append-only audit logs (“tape-first”) aligned with Republic’s
// tape schema (https://github.com/bubbuild/republic, src/republic/tape; concepts at https://getrepublic.org).
package tape

import (
	"time"
)

type Entry struct {
	ID      int64          `json:"id"`
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	Meta    map[string]any `json:"meta,omitempty"`
	Date    string         `json:"date"`
}

func newEntry(kind string, payload, meta map[string]any) Entry {
	if payload == nil {
		payload = map[string]any{}
	}
	if meta == nil {
		meta = map[string]any{}
	}
	return Entry{
		ID:      0,
		Kind:    kind,
		Payload: payload,
		Meta:    meta,
		Date:    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func Message(msg, meta map[string]any) Entry {
	p := map[string]any{}
	for k, v := range msg {
		p[k] = v
	}
	return newEntry("message", p, meta)
}

func System(content string, meta map[string]any) Entry {
	return newEntry("system", map[string]any{"content": content}, meta)
}

func Anchor(name string, state, meta map[string]any) Entry {
	p := map[string]any{"name": name}
	if state != nil {
		p["state"] = state
	}
	return newEntry("anchor", p, meta)
}

func ToolCall(calls []map[string]any, meta map[string]any) Entry {
	return newEntry("tool_call", map[string]any{"calls": calls}, meta)
}

func ToolResult(results []any, meta map[string]any) Entry {
	return newEntry("tool_result", map[string]any{"results": results}, meta)
}

func ErrorPayload(message string, extra, meta map[string]any) Entry {
	p := map[string]any{"message": message}
	for k, v := range extra {
		p[k] = v
	}
	return newEntry("error", p, meta)
}

func Event(name string, data, meta map[string]any) Entry {
	p := map[string]any{"name": name}
	if data != nil {
		p["data"] = data
	}
	return newEntry("event", p, meta)
}
