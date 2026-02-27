package querier

import (
	"context"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/store"
)

func setupQuerier(t *testing.T) *Querier {
	t.Helper()
	ctx := context.Background()
	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatalf("NewDuckDBStore: %v", err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	patterns := []store.Pattern{
		{PatternID: "login", PatternType: "drain", RawPattern: "login user=<*>"},
		{PatternID: "error", PatternType: "drain", RawPattern: "error <*>"},
	}
	if err := s.InsertPatterns(ctx, patterns); err != nil {
		t.Fatalf("InsertPatterns: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []store.LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "login user=alice", PatternID: "login"},
		{LineNumber: 2, Timestamp: ts.Add(time.Second), Raw: "login user=bob", PatternID: "login"},
		{LineNumber: 3, Timestamp: ts.Add(2 * time.Second), Raw: "error timeout", PatternID: "error"},
		{LineNumber: 4, Timestamp: ts.Add(3 * time.Second), Raw: "login user=carol", PatternID: "login"},
	}
	if err := s.InsertLogBatch(ctx, entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	return NewQuerier(s)
}

func TestByPattern(t *testing.T) {
	q := setupQuerier(t)
	ctx := context.Background()

	results, err := q.ByPattern(ctx, "login")
	if err != nil {
		t.Fatalf("ByPattern: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 login entries, got %d", len(results))
	}

	results, err = q.ByPattern(ctx, "error")
	if err != nil {
		t.Fatalf("ByPattern error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 error entry, got %d", len(results))
	}
}

func TestSummary(t *testing.T) {
	q := setupQuerier(t)
	ctx := context.Background()

	summaries, err := q.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	if summaries[0].PatternID != "login" || summaries[0].Count != 3 {
		t.Errorf("first summary: got %+v, want login with count 3", summaries[0])
	}
	if summaries[1].PatternID != "error" || summaries[1].Count != 1 {
		t.Errorf("second summary: got %+v, want error with count 1", summaries[1])
	}
}

func TestSearch(t *testing.T) {
	q := setupQuerier(t)
	ctx := context.Background()

	results, err := q.Search(ctx, store.QueryOpts{Limit: 2})
	if err != nil {
		t.Fatalf("Search limit: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit, got %d", len(results))
	}

	results, err = q.Search(ctx, store.QueryOpts{PatternID: "error"})
	if err != nil {
		t.Fatalf("Search pattern: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 error result, got %d", len(results))
	}
}
