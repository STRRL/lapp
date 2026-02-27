package querier

import (
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/store"
)

func setupQuerier(t *testing.T) *Querier {
	t.Helper()
	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatalf("NewDuckDBStore: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []store.LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "login user=alice", TemplateID: "login", Template: "login user=<*>"},
		{LineNumber: 2, Timestamp: ts.Add(time.Second), Raw: "login user=bob", TemplateID: "login", Template: "login user=<*>"},
		{LineNumber: 3, Timestamp: ts.Add(2 * time.Second), Raw: "error timeout", TemplateID: "error", Template: "error <*>"},
		{LineNumber: 4, Timestamp: ts.Add(3 * time.Second), Raw: "login user=carol", TemplateID: "login", Template: "login user=<*>"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	return NewQuerier(s)
}

func TestByTemplate(t *testing.T) {
	q := setupQuerier(t)

	results, err := q.ByTemplate("login")
	if err != nil {
		t.Fatalf("ByTemplate: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 login entries, got %d", len(results))
	}

	results, err = q.ByTemplate("error")
	if err != nil {
		t.Fatalf("ByTemplate error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 error entry, got %d", len(results))
	}
}

func TestSummary(t *testing.T) {
	q := setupQuerier(t)

	summaries, err := q.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// Ordered by count descending
	if summaries[0].TemplateID != "login" || summaries[0].Count != 3 {
		t.Errorf("first summary: got %+v, want login with count 3", summaries[0])
	}
	if summaries[1].TemplateID != "error" || summaries[1].Count != 1 {
		t.Errorf("second summary: got %+v, want error with count 1", summaries[1])
	}
}

func TestSearch(t *testing.T) {
	q := setupQuerier(t)

	// Search with limit
	results, err := q.Search(store.QueryOpts{Limit: 2})
	if err != nil {
		t.Fatalf("Search limit: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit, got %d", len(results))
	}

	// Search by template
	results, err = q.Search(store.QueryOpts{TemplateID: "error"})
	if err != nil {
		t.Fatalf("Search template: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 error result, got %d", len(results))
	}
}
