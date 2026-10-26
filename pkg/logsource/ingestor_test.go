package logsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	for rr := range ch {
		if rr.Err != nil {
			t.Fatalf("unexpected error: %v", rr.Err)
		}
		got = append(got, *rr.Value)
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

func TestScanLinesLongLines(t *testing.T) {
	long := strings.Repeat("a", 200<<10)
	huge := strings.Repeat("b", MaxLineBytes+1<<20)
	input := "first\r\n" + long + "\n" + huge + "\nlast"

	type line struct {
		size      int
		truncated bool
	}
	var got []line
	err := ScanLines(strings.NewReader(input), func(l string, truncated bool) error {
		got = append(got, line{len(l), truncated})
		return nil
	})
	if err != nil {
		t.Fatalf("ScanLines: %v", err)
	}
	want := []line{{5, false}, {len(long), false}, {MaxLineBytes, true}, {4, false}}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i+1, got[i], want[i])
		}
	}
}
