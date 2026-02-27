package store

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *DuckDBStore {
	t.Helper()
	s, err := NewDuckDBStore("")
	if err != nil {
		t.Fatalf("NewDuckDBStore: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInit(t *testing.T) {
	s := newTestStore(t)

	// Verify table exists by querying it
	rows, err := s.db.Query("SELECT COUNT(*) FROM log_entries")
	if err != nil {
		t.Fatalf("query after init: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatalf("scan count: %v", err)
		}
	}
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}
}

func TestInsertAndQueryByPattern(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	entry := LogEntry{
		LineNumber: 1,
		Timestamp:  ts,
		Raw:        "INFO Starting server on port 8080",
		PatternID:  "D1",
	}

	if err := s.InsertLog(entry); err != nil {
		t.Fatalf("InsertLog: %v", err)
	}

	results, err := s.QueryByPattern("D1")
	if err != nil {
		t.Fatalf("QueryByPattern: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.LineNumber != 1 {
		t.Errorf("LineNumber: got %d, want 1", r.LineNumber)
	}
	if r.Raw != entry.Raw {
		t.Errorf("Raw: got %q, want %q", r.Raw, entry.Raw)
	}
	if r.PatternID != "D1" {
		t.Errorf("PatternID: got %q, want %q", r.PatternID, "D1")
	}

	// Query non-existent pattern returns empty
	empty, err := s.QueryByPattern("no-such-pattern")
	if err != nil {
		t.Fatalf("QueryByPattern empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 results for missing pattern, got %d", len(empty))
	}
}

func TestInsertLogBatch(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", PatternID: "a"},
		{LineNumber: 2, Timestamp: ts.Add(time.Second), Raw: "line 2", PatternID: "a"},
		{LineNumber: 3, Timestamp: ts.Add(2 * time.Second), Raw: "line 3", PatternID: "b"},
	}

	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	aResults, err := s.QueryByPattern("a")
	if err != nil {
		t.Fatalf("QueryByPattern a: %v", err)
	}
	if len(aResults) != 2 {
		t.Errorf("expected 2 results for pattern a, got %d", len(aResults))
	}

	bResults, err := s.QueryByPattern("b")
	if err != nil {
		t.Fatalf("QueryByPattern b: %v", err)
	}
	if len(bResults) != 1 {
		t.Errorf("expected 1 result for pattern b, got %d", len(bResults))
	}
}

func TestQueryLogs(t *testing.T) {
	s := newTestStore(t)

	base := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: base, Raw: "line 1", PatternID: "a"},
		{LineNumber: 2, Timestamp: base.Add(time.Minute), Raw: "line 2", PatternID: "b"},
		{LineNumber: 3, Timestamp: base.Add(2 * time.Minute), Raw: "line 3", PatternID: "a"},
		{LineNumber: 4, Timestamp: base.Add(3 * time.Minute), Raw: "line 4", PatternID: "c"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	// Filter by pattern
	results, err := s.QueryLogs(QueryOpts{PatternID: "a"})
	if err != nil {
		t.Fatalf("QueryLogs pattern filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("pattern filter: expected 2, got %d", len(results))
	}

	// Filter by time range
	results, err = s.QueryLogs(QueryOpts{
		From: base.Add(30 * time.Second),
		To:   base.Add(2*time.Minute + 30*time.Second),
	})
	if err != nil {
		t.Fatalf("QueryLogs time filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("time filter: expected 2, got %d", len(results))
	}

	// Limit
	results, err = s.QueryLogs(QueryOpts{Limit: 2})
	if err != nil {
		t.Fatalf("QueryLogs limit: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("limit: expected 2, got %d", len(results))
	}

	// No filters returns all
	results, err = s.QueryLogs(QueryOpts{})
	if err != nil {
		t.Fatalf("QueryLogs no filter: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("no filter: expected 4, got %d", len(results))
	}
}

func TestPatternSummaries(t *testing.T) {
	s := newTestStore(t)

	patterns := []Pattern{
		{PatternID: "a", PatternType: "drain", RawPattern: "pattern a"},
		{PatternID: "b", PatternType: "drain", RawPattern: "pattern b"},
		{PatternID: "c", PatternType: "drain", RawPattern: "pattern c"},
	}
	if err := s.InsertPatterns(patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", PatternID: "a"},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", PatternID: "a"},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", PatternID: "a"},
		{LineNumber: 4, Timestamp: ts, Raw: "line 4", PatternID: "b"},
		{LineNumber: 5, Timestamp: ts, Raw: "line 5", PatternID: "b"},
		{LineNumber: 6, Timestamp: ts, Raw: "line 6", PatternID: "c"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	summaries, err := s.PatternSummaries()
	if err != nil {
		t.Fatalf("PatternSummaries: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}

	// Ordered by count desc
	if summaries[0].PatternID != "a" || summaries[0].Count != 3 {
		t.Errorf("first summary: got %+v, want pattern a with count 3", summaries[0])
	}
	if summaries[1].PatternID != "b" || summaries[1].Count != 2 {
		t.Errorf("second summary: got %+v, want pattern b with count 2", summaries[1])
	}
	if summaries[2].PatternID != "c" || summaries[2].Count != 1 {
		t.Errorf("third summary: got %+v, want pattern c with count 1", summaries[2])
	}
}

func TestInsertAndQueryPatterns(t *testing.T) {
	s := newTestStore(t)

	patterns := []Pattern{
		{PatternID: "D1", PatternType: "drain", RawPattern: "Starting <*> on port <*>"},
		{PatternID: "D2", PatternType: "drain", RawPattern: "Connection timeout after <*> ms"},
	}

	if err := s.InsertPatterns(patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	got, err := s.Patterns()
	if err != nil {
		t.Fatalf("Patterns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(got))
	}

	if got[0].PatternID != "D1" || got[0].PatternType != "drain" {
		t.Errorf("first pattern: got %+v", got[0])
	}
}

func TestUpdatePatternLabels(t *testing.T) {
	s := newTestStore(t)

	patterns := []Pattern{
		{PatternID: "D1", PatternType: "drain", RawPattern: "Starting <*> on port <*>"},
		{PatternID: "D2", PatternType: "drain", RawPattern: "Connection timeout after <*> ms"},
	}
	if err := s.InsertPatterns(patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	labels := []Pattern{
		{PatternID: "D1", SemanticID: "server-startup", Description: "Server starting on a port"},
		{PatternID: "D2", SemanticID: "conn-timeout", Description: "Connection timeout"},
	}
	if err := s.UpdatePatternLabels(labels); err != nil {
		t.Fatalf("UpdatePatternLabels: %v", err)
	}

	got, err := s.Patterns()
	if err != nil {
		t.Fatalf("Patterns: %v", err)
	}
	if got[0].SemanticID != "server-startup" {
		t.Errorf("D1 semantic_id: got %q, want %q", got[0].SemanticID, "server-startup")
	}
	if got[1].SemanticID != "conn-timeout" {
		t.Errorf("D2 semantic_id: got %q, want %q", got[1].SemanticID, "conn-timeout")
	}
}

func TestPatternSummariesWithPatterns(t *testing.T) {
	s := newTestStore(t)

	patterns := []Pattern{
		{PatternID: "D1", PatternType: "drain", RawPattern: "Starting <*> on port <*>", SemanticID: "server-startup", Description: "Server starting"},
	}
	if err := s.InsertPatterns(patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", PatternID: "D1"},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", PatternID: "D1"},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", PatternID: "X1"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	summaries, err := s.PatternSummaries()
	if err != nil {
		t.Fatalf("PatternSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary (only persisted patterns), got %d", len(summaries))
	}

	if summaries[0].PatternID != "D1" {
		t.Fatalf("expected D1 first, got %s", summaries[0].PatternID)
	}
	if summaries[0].PatternType != "drain" {
		t.Errorf("D1 PatternType: got %q, want %q", summaries[0].PatternType, "drain")
	}
	if summaries[0].SemanticID != "server-startup" {
		t.Errorf("D1 SemanticID: got %q, want %q", summaries[0].SemanticID, "server-startup")
	}
	if summaries[0].Pattern != "Starting <*> on port <*>" {
		t.Errorf("D1 Pattern: got %q, want from patterns table", summaries[0].Pattern)
	}
	if summaries[0].Count != 2 {
		t.Errorf("D1 Count: got %d, want 2", summaries[0].Count)
	}
}

func TestPatternCounts(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", PatternID: "D1"},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", PatternID: "D1"},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", PatternID: "D1"},
		{LineNumber: 4, Timestamp: ts, Raw: "line 4", PatternID: "D2"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	counts, err := s.PatternCounts()
	if err != nil {
		t.Fatalf("PatternCounts: %v", err)
	}
	if counts["D1"] != 3 {
		t.Errorf("D1 count: got %d, want 3", counts["D1"])
	}
	if counts["D2"] != 1 {
		t.Errorf("D2 count: got %d, want 1", counts["D2"])
	}
}
