package multiline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestData(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	return lines
}

func TestIntegrationJavaStackTrace(t *testing.T) {
	lines := readTestData(t, "java_stacktrace.log")
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)

	// Expected: 4 entries
	// 1. "2024-03-28 13:45:30 INFO  Application started successfully"
	// 2. "2024-03-28 13:45:31 DEBUG Processing request..."
	// 3. "2024-03-28 13:45:32 ERROR..." + stack trace (lines 3-11)
	// 4. "2024-03-28 13:45:33 WARN..."
	// 5. "2024-03-28 13:45:34 INFO..."
	if len(merged) != 5 {
		for i, m := range merged {
			t.Logf("entry %d: lines %d-%d: %s", i, m.StartLine, m.EndLine, truncate(m.Content))
		}
		t.Fatalf("expected 5 merged entries, got %d", len(merged))
	}

	// The stack trace entry should span multiple lines
	stackEntry := merged[2]
	if stackEntry.StartLine == stackEntry.EndLine {
		t.Error("stack trace entry should span multiple lines")
	}
	if !strings.Contains(stackEntry.Content, "NullPointerException") {
		t.Error("stack trace entry should contain NullPointerException")
	}
	if !strings.Contains(stackEntry.Content, "Caused by:") {
		t.Error("stack trace entry should contain Caused by:")
	}
}

func TestIntegrationPythonTraceback(t *testing.T) {
	lines := readTestData(t, "python_traceback.log")
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)

	// Expected: 4 entries
	// 1. "2024-03-28 14:00:01 INFO Starting batch processing"
	// 2. "2024-03-28 14:00:02 ERROR..." + traceback (lines 2-8)
	// 3. "2024-03-28 14:00:03 INFO Worker thread restarted"
	// 4. "2024-03-28 14:00:04 DEBUG Processing next batch..."
	if len(merged) != 4 {
		for i, m := range merged {
			t.Logf("entry %d: lines %d-%d: %s", i, m.StartLine, m.EndLine, truncate(m.Content))
		}
		t.Fatalf("expected 4 merged entries, got %d", len(merged))
	}

	tracebackEntry := merged[1]
	if !strings.Contains(tracebackEntry.Content, "Traceback") {
		t.Error("traceback entry should contain Traceback")
	}
	if !strings.Contains(tracebackEntry.Content, "ZeroDivisionError") {
		t.Error("traceback entry should contain ZeroDivisionError")
	}
}

func TestIntegrationGoPanic(t *testing.T) {
	lines := readTestData(t, "go_panic.log")
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)

	// Expected: 3 entries
	// 1. "2024-03-28 15:00:01 INFO Server listening..."
	// 2. "2024-03-28 15:00:05 INFO Received request..."
	// 3. "2024-03-28 15:00:05 FATAL panic:..." + goroutine dump (lines 3-10)
	// 4. "2024-03-28 15:00:06 INFO Server recovered..."
	if len(merged) != 4 {
		for i, m := range merged {
			t.Logf("entry %d: lines %d-%d: %s", i, m.StartLine, m.EndLine, truncate(m.Content))
		}
		t.Fatalf("expected 4 merged entries, got %d", len(merged))
	}

	panicEntry := merged[2]
	if !strings.Contains(panicEntry.Content, "panic:") {
		t.Error("panic entry should contain panic:")
	}
	if !strings.Contains(panicEntry.Content, "goroutine") {
		t.Error("panic entry should contain goroutine dump")
	}
}

func TestIntegrationSingleLine(t *testing.T) {
	lines := readTestData(t, "single_line.log")
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)

	if len(merged) != len(lines) {
		t.Fatalf("expected %d entries for single-line logs, got %d", len(lines), len(merged))
	}

	for i, m := range merged {
		if m.StartLine != m.EndLine {
			t.Errorf("entry %d: expected single-line, got %d-%d", i, m.StartLine, m.EndLine)
		}
		if m.Content != lines[i] {
			t.Errorf("entry %d: content mismatch", i)
		}
	}
}

func TestIntegrationMixedFormats(t *testing.T) {
	lines := readTestData(t, "mixed_formats.log")
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)

	// All lines in mixed_formats.log start with timestamps,
	// so each should be its own entry
	if len(merged) != len(lines) {
		for i, m := range merged {
			t.Logf("entry %d: lines %d-%d: %s", i, m.StartLine, m.EndLine, truncate(m.Content))
		}
		t.Fatalf("expected %d entries, got %d", len(lines), len(merged))
	}
}

func truncate(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "..."
}
