package multiline

import (
	"testing"

	"github.com/strrl/lapp/pkg/ingestor"
)

func TestMergeSliceJavaStackTrace(t *testing.T) {
	lines := []string{
		"2024-03-28 13:45:30 INFO  Application started successfully",
		"2024-03-28 13:45:32 ERROR NullPointerException occurred",
		"java.lang.NullPointerException: Cannot invoke method",
		"\tat com.example.service.UserService.getUser(UserService.java:42)",
		"\tat com.example.controller.UserController.handleRequest(UserController.java:87)",
		"Caused by: java.lang.IllegalStateException: Database connection is null",
		"\tat com.example.db.ConnectionPool.getConnection(ConnectionPool.java:31)",
		"\t... 2 more",
		"2024-03-28 13:45:33 WARN  Retrying request after failure",
	}

	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged entries, got %d", len(merged))
	}

	if merged[0].StartLine != 1 || merged[0].EndLine != 1 {
		t.Errorf("entry 0: expected lines 1-1, got %d-%d", merged[0].StartLine, merged[0].EndLine)
	}

	if merged[1].StartLine != 2 || merged[1].EndLine != 8 {
		t.Errorf("entry 1: expected lines 2-8, got %d-%d", merged[1].StartLine, merged[1].EndLine)
	}

	if merged[2].StartLine != 9 || merged[2].EndLine != 9 {
		t.Errorf("entry 2: expected lines 9-9, got %d-%d", merged[2].StartLine, merged[2].EndLine)
	}
}

func TestMergeSliceSingleLine(t *testing.T) {
	lines := []string{
		"2024-03-28 10:00:01 INFO  Application starting",
		"2024-03-28 10:00:02 INFO  Loading configuration",
		"2024-03-28 10:00:03 DEBUG Database connection established",
	}

	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries for single-line logs, got %d", len(merged))
	}

	for i, m := range merged {
		if m.StartLine != m.EndLine {
			t.Errorf("entry %d: expected single-line (start==end), got %d-%d", i, m.StartLine, m.EndLine)
		}
	}
}

func TestMergeSliceEmpty(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(nil, d)
	if merged != nil {
		t.Errorf("expected nil for empty input, got %v", merged)
	}
}

func TestMergeChannel(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan ingestor.Result[*ingestor.LogLine], 10)
	lines := []*ingestor.LogLine{
		{LineNumber: 1, Content: "2024-03-28 13:45:30 INFO started"},
		{LineNumber: 2, Content: "2024-03-28 13:45:31 ERROR something broke"},
		{LineNumber: 3, Content: "\tat com.example.Foo.bar(Foo.java:42)"},
		{LineNumber: 4, Content: "2024-03-28 13:45:32 INFO recovered"},
	}

	for _, l := range lines {
		ch <- ingestor.Result[*ingestor.LogLine]{Value: l}
	}
	close(ch)

	merged := Merge(ch, d)
	var results []MergedLine
	for m := range merged {
		if m.Err != nil {
			t.Fatalf("unexpected error: %v", m.Err)
		}
		results = append(results, *m.Value)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 merged entries, got %d", len(results))
	}

	if results[1].StartLine != 2 || results[1].EndLine != 3 {
		t.Errorf("entry 1: expected lines 2-3, got %d-%d", results[1].StartLine, results[1].EndLine)
	}
}

func TestMergeSliceMaxEntryBytes(t *testing.T) {
	d, err := NewDetector(DetectorConfig{
		MaxEntryBytes: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{
		"2024-03-28 13:45:30 INFO started",
		"this is a continuation line that will push us over the limit with more text",
		"another continuation line",
	}

	merged := MergeSlice(lines, d)
	if len(merged) < 2 {
		t.Fatalf("expected at least 2 entries due to max entry bytes, got %d", len(merged))
	}
}

func TestMergeSliceNoTimestamp(t *testing.T) {
	// Lines with no recognizable timestamps should pass through one per entry
	lines := []string{
		"plain log line one",
		"plain log line two",
		"plain log line three",
	}

	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	merged := MergeSlice(lines, d)
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries for non-timestamp logs, got %d", len(merged))
	}
	for i, m := range merged {
		if m.StartLine != m.EndLine {
			t.Errorf("entry %d: expected single-line, got %d-%d", i, m.StartLine, m.EndLine)
		}
	}
}

func TestMergeChannelNoTimestamp(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan ingestor.Result[*ingestor.LogLine], 10)
	for i, s := range []string{"foo", "bar", "baz"} {
		ch <- ingestor.Result[*ingestor.LogLine]{Value: &ingestor.LogLine{LineNumber: i + 1, Content: s}}
	}
	close(ch)

	var results []MergedLine
	for m := range Merge(ch, d) {
		if m.Err != nil {
			t.Fatalf("unexpected error: %v", m.Err)
		}
		results = append(results, *m.Value)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 entries for non-timestamp logs, got %d", len(results))
	}
}

func TestMergeSliceOverflowLineRange(t *testing.T) {
	d, err := NewDetector(DetectorConfig{
		MaxEntryBytes: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{
		"2024-03-28 13:45:30 INFO started",
		"continuation that pushes over the byte limit for this entry easily",
		"2024-03-28 13:45:31 INFO next entry",
	}

	merged := MergeSlice(lines, d)

	// Verify no overlapping line ranges
	for i := 1; i < len(merged); i++ {
		if merged[i].StartLine <= merged[i-1].EndLine {
			t.Errorf("entries %d and %d have overlapping line ranges: %d-%d vs %d-%d",
				i-1, i, merged[i-1].StartLine, merged[i-1].EndLine,
				merged[i].StartLine, merged[i].EndLine)
		}
	}
}
