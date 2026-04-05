package tape

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONLStoreAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tape.jsonl")

	store := NewJSONLStore(path)

	if err := store.Append(MessageEntry("user", "hello", nil)); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := store.Append(MessageEntry("assistant", "hi there", map[string]any{"model": "test"})); err != nil {
		t.Fatalf("append response: %v", err)
	}
	if err := store.Append(ErrorEntry("test", "something failed", nil)); err != nil {
		t.Fatalf("append error: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		entries = append(entries, e)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	if entries[0].ID != 1 || entries[0].Kind != KindMessage {
		t.Errorf("entry 0: id=%d kind=%s", entries[0].ID, entries[0].Kind)
	}
	if entries[1].ID != 2 || entries[1].Payload["role"] != "assistant" {
		t.Errorf("entry 1: id=%d role=%v", entries[1].ID, entries[1].Payload["role"])
	}
	if entries[2].Kind != KindError {
		t.Errorf("entry 2: kind=%s", entries[2].Kind)
	}
}
