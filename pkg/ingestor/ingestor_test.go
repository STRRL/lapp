package ingestor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIngest(t *testing.T) {
	lines := []string{
		"2024-01-01 INFO Starting service",
		"2024-01-01 WARN Disk space low",
		"2024-01-01 ERROR Connection refused",
		"2024-01-01 INFO Retry succeeded",
		"2024-01-01 DEBUG Heartbeat OK",
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.log")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	for _, l := range lines {
		_, _ = f.WriteString(l + "\n")
	}
	_ = f.Close()

	ch, err := Ingest(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Ingest returned error: %v", err)
	}

	var got []LogLine
	for ll := range ch {
		got = append(got, ll)
	}

	if len(got) != len(lines) {
		t.Fatalf("expected %d lines, got %d", len(lines), len(got))
	}

	for i, ll := range got {
		expectedNum := i + 1
		if ll.LineNumber != expectedNum {
			t.Errorf("line %d: expected LineNumber %d, got %d", i, expectedNum, ll.LineNumber)
		}
		if ll.Content != lines[i] {
			t.Errorf("line %d: expected Content %q, got %q", i, lines[i], ll.Content)
		}
	}
}

func TestIngestFileNotFound(t *testing.T) {
	_, err := Ingest(context.Background(), "/nonexistent/path/to/file.log")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}
