package tape

import "time"

// Entry is a single append-only entry in a tape, modeled after republic's TapeEntry.
type Entry struct {
	ID      int            `json:"id"`
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	Meta    map[string]any `json:"meta,omitempty"`
	Date    string         `json:"date"`
}

// Entry kinds.
const (
	KindMessage    = "message"
	KindSystem     = "system"
	KindToolCall   = "tool_call"
	KindToolResult = "tool_result"
	KindError      = "error"
	KindEvent      = "event"
)

func newEntry(kind string, payload, meta map[string]any) Entry {
	return Entry{
		Kind:    kind,
		Payload: payload,
		Meta:    meta,
		Date:    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// MessageEntry creates a message entry.
func MessageEntry(role, content string, meta map[string]any) Entry {
	return newEntry(KindMessage, map[string]any{
		"role":    role,
		"content": content,
	}, meta)
}

// SystemEntry creates a system prompt entry.
func SystemEntry(content string, meta map[string]any) Entry {
	return newEntry(KindSystem, map[string]any{
		"content": content,
	}, meta)
}

// ToolCallEntry creates a tool call entry.
func ToolCallEntry(calls []map[string]any, meta map[string]any) Entry {
	return newEntry(KindToolCall, map[string]any{
		"calls": calls,
	}, meta)
}

// ToolResultEntry creates a tool result entry.
func ToolResultEntry(results []any, meta map[string]any) Entry {
	return newEntry(KindToolResult, map[string]any{
		"results": results,
	}, meta)
}

// ErrorEntry creates an error entry.
func ErrorEntry(kind, message string, meta map[string]any) Entry {
	return newEntry(KindError, map[string]any{
		"kind":    kind,
		"message": message,
	}, meta)
}

// EventEntry creates a generic event entry.
func EventEntry(name string, data, meta map[string]any) Entry {
	return newEntry(KindEvent, map[string]any{
		"name": name,
		"data": data,
	}, meta)
}
