package store

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *DuckDBStore {
	t.Helper()
	s, err := NewDuckDBStore("")
	if err != nil {
		t.Fatalf("NewDuckDBStore: %v", err)
	}
	if err := s.Init(context.Background()); err != nil {
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
	ctx := context.Background()

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	entry := LogEntry{
		LineNumber: 1,
		Timestamp:  ts,
		Raw:        "INFO Starting server on port 8080",
		Labels:     map[string]string{"pattern": "server-startup"},
	}

	if err := s.InsertLog(ctx, entry); err != nil {
		t.Fatalf("InsertLog: %v", err)
	}

	results, err := s.QueryByPattern(ctx, "server-startup")
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
	if r.Labels["pattern"] != "server-startup" {
		t.Errorf("Labels[pattern]: got %q, want %q", r.Labels["pattern"], "server-startup")
	}

	// Query non-existent pattern returns empty
	empty, err := s.QueryByPattern(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("QueryByPattern empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 results for missing pattern, got %d", len(empty))
	}
}

func TestInsertLogBatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", Labels: map[string]string{"pattern": "pat-a"}},
		{LineNumber: 2, Timestamp: ts.Add(time.Second), Raw: "line 2", Labels: map[string]string{"pattern": "pat-a"}},
		{LineNumber: 3, Timestamp: ts.Add(2 * time.Second), Raw: "line 3", Labels: map[string]string{"pattern": "pat-b"}},
	}

	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	aResults, err := s.QueryByPattern(ctx, "pat-a")
	if err != nil {
		t.Fatalf("QueryByPattern a: %v", err)
	}
	if len(aResults) != 2 {
		t.Errorf("expected 2 results for pattern a, got %d", len(aResults))
	}

	bResults, err := s.QueryByPattern(ctx, "pat-b")
	if err != nil {
		t.Fatalf("QueryByPattern b: %v", err)
	}
	if len(bResults) != 1 {
		t.Errorf("expected 1 result for pattern b, got %d", len(bResults))
	}
}

func TestQueryLogs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: base, Raw: "line 1", Labels: map[string]string{"pattern": "pat-a"}},
		{LineNumber: 2, Timestamp: base.Add(time.Minute), Raw: "line 2", Labels: map[string]string{"pattern": "pat-b"}},
		{LineNumber: 3, Timestamp: base.Add(2 * time.Minute), Raw: "line 3", Labels: map[string]string{"pattern": "pat-a"}},
		{LineNumber: 4, Timestamp: base.Add(3 * time.Minute), Raw: "line 4", Labels: map[string]string{"pattern": "pat-c"}},
	}
	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	// Filter by pattern
	results, err := s.QueryLogs(ctx, QueryOpts{Pattern: "pat-a"})
	if err != nil {
		t.Fatalf("QueryLogs pattern filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("pattern filter: expected 2, got %d", len(results))
	}

	// Filter by time range
	results, err = s.QueryLogs(ctx, QueryOpts{
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
	results, err = s.QueryLogs(ctx, QueryOpts{Limit: 2})
	if err != nil {
		t.Fatalf("QueryLogs limit: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("limit: expected 2, got %d", len(results))
	}

	// No filters returns all
	results, err = s.QueryLogs(ctx, QueryOpts{})
	if err != nil {
		t.Fatalf("QueryLogs no filter: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("no filter: expected 4, got %d", len(results))
	}
}

func TestPatternSummaries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	patterns := []Pattern{
		{PatternUUIDString: "00000000-0000-0000-0000-00000000000a", PatternType: "drain", RawPattern: "pattern a", SemanticID: "pat-a"},
		{PatternUUIDString: "00000000-0000-0000-0000-00000000000b", PatternType: "drain", RawPattern: "pattern b", SemanticID: "pat-b"},
		{PatternUUIDString: "00000000-0000-0000-0000-00000000000c", PatternType: "drain", RawPattern: "pattern c", SemanticID: "pat-c"},
	}
	if err := s.InsertPatterns(ctx, patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", Labels: map[string]string{"pattern": "pat-a", "pattern_id": "00000000-0000-0000-0000-00000000000a"}},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", Labels: map[string]string{"pattern": "pat-a", "pattern_id": "00000000-0000-0000-0000-00000000000a"}},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", Labels: map[string]string{"pattern": "pat-a", "pattern_id": "00000000-0000-0000-0000-00000000000a"}},
		{LineNumber: 4, Timestamp: ts, Raw: "line 4", Labels: map[string]string{"pattern": "pat-b", "pattern_id": "00000000-0000-0000-0000-00000000000b"}},
		{LineNumber: 5, Timestamp: ts, Raw: "line 5", Labels: map[string]string{"pattern": "pat-b", "pattern_id": "00000000-0000-0000-0000-00000000000b"}},
		{LineNumber: 6, Timestamp: ts, Raw: "line 6", Labels: map[string]string{"pattern": "pat-c", "pattern_id": "00000000-0000-0000-0000-00000000000c"}},
	}
	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	summaries, err := s.PatternSummaries(ctx)
	if err != nil {
		t.Fatalf("PatternSummaries: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}

	// Ordered by count desc
	if summaries[0].SemanticID != "pat-a" || summaries[0].Count != 3 {
		t.Errorf("first summary: got %+v, want pat-a with count 3", summaries[0])
	}
	if summaries[1].SemanticID != "pat-b" || summaries[1].Count != 2 {
		t.Errorf("second summary: got %+v, want pat-b with count 2", summaries[1])
	}
	if summaries[2].SemanticID != "pat-c" || summaries[2].Count != 1 {
		t.Errorf("third summary: got %+v, want pat-c with count 1", summaries[2])
	}
}

func TestInsertAndQueryPatterns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	patterns := []Pattern{
		{PatternUUIDString: "00000000-0000-0000-0000-000000000001", PatternType: "drain", RawPattern: "Starting <*> on port <*>"},
		{PatternUUIDString: "00000000-0000-0000-0000-000000000002", PatternType: "drain", RawPattern: "Connection timeout after <*> ms"},
	}

	if err := s.InsertPatterns(ctx, patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	got, err := s.Patterns(ctx)
	if err != nil {
		t.Fatalf("Patterns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(got))
	}

	if got[0].PatternUUIDString != "00000000-0000-0000-0000-000000000001" || got[0].PatternType != "drain" {
		t.Errorf("first pattern: got %+v", got[0])
	}
}

func TestPatternSummariesWithPatterns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	patterns := []Pattern{
		{PatternUUIDString: "00000000-0000-0000-0000-000000000001", PatternType: "drain", RawPattern: "Starting <*> on port <*>", SemanticID: "server-startup", Description: "Server starting"},
	}
	if err := s.InsertPatterns(ctx, patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", Labels: map[string]string{"pattern": "server-startup", "pattern_id": "00000000-0000-0000-0000-000000000001"}},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", Labels: map[string]string{"pattern": "server-startup", "pattern_id": "00000000-0000-0000-0000-000000000001"}},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", Labels: map[string]string{"pattern": "unmatched", "pattern_id": "00000000-0000-0000-0000-000000000099"}},
	}
	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	summaries, err := s.PatternSummaries(ctx)
	if err != nil {
		t.Fatalf("PatternSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary (only persisted patterns), got %d", len(summaries))
	}

	if summaries[0].SemanticID != "server-startup" {
		t.Fatalf("expected server-startup, got %s", summaries[0].SemanticID)
	}
	if summaries[0].PatternType != "drain" {
		t.Errorf("PatternType: got %q, want %q", summaries[0].PatternType, "drain")
	}
	if summaries[0].Pattern != "Starting <*> on port <*>" {
		t.Errorf("Pattern: got %q, want from patterns table", summaries[0].Pattern)
	}
	if summaries[0].Count != 2 {
		t.Errorf("Count: got %d, want 2", summaries[0].Count)
	}
}

func TestPatternCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", Labels: map[string]string{"pattern": "pat-1"}},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", Labels: map[string]string{"pattern": "pat-1"}},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", Labels: map[string]string{"pattern": "pat-1"}},
		{LineNumber: 4, Timestamp: ts, Raw: "line 4", Labels: map[string]string{"pattern": "pat-2"}},
	}
	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	counts, err := s.PatternCounts(ctx)
	if err != nil {
		t.Fatalf("PatternCounts: %v", err)
	}
	if counts["pat-1"] != 3 {
		t.Errorf("pattern 1 count: got %d, want 3", counts["pat-1"])
	}
	if counts["pat-2"] != 1 {
		t.Errorf("pattern 2 count: got %d, want 1", counts["pat-2"])
	}
}
