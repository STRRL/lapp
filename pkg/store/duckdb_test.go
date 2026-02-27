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

func TestInsertAndQueryByTemplate(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	entry := LogEntry{
		LineNumber: 1,
		Timestamp:  ts,
		Raw:        "INFO Starting server on port 8080",
		TemplateID: "tpl-001",
		Template:   "INFO Starting server on port <*>",
	}

	if err := s.InsertLog(entry); err != nil {
		t.Fatalf("InsertLog: %v", err)
	}

	results, err := s.QueryByTemplate("tpl-001")
	if err != nil {
		t.Fatalf("QueryByTemplate: %v", err)
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
	if r.TemplateID != "tpl-001" {
		t.Errorf("TemplateID: got %q, want %q", r.TemplateID, "tpl-001")
	}
	if r.Template != entry.Template {
		t.Errorf("Template: got %q, want %q", r.Template, entry.Template)
	}

	// Query non-existent template returns empty
	empty, err := s.QueryByTemplate("no-such-template")
	if err != nil {
		t.Fatalf("QueryByTemplate empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 results for missing template, got %d", len(empty))
	}
}

func TestInsertLogBatch(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 2, Timestamp: ts.Add(time.Second), Raw: "line 2", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 3, Timestamp: ts.Add(2 * time.Second), Raw: "line 3", TemplateID: "b", Template: "tpl b"},
	}

	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	aResults, err := s.QueryByTemplate("a")
	if err != nil {
		t.Fatalf("QueryByTemplate a: %v", err)
	}
	if len(aResults) != 2 {
		t.Errorf("expected 2 results for template a, got %d", len(aResults))
	}

	bResults, err := s.QueryByTemplate("b")
	if err != nil {
		t.Fatalf("QueryByTemplate b: %v", err)
	}
	if len(bResults) != 1 {
		t.Errorf("expected 1 result for template b, got %d", len(bResults))
	}
}

func TestQueryLogs(t *testing.T) {
	s := newTestStore(t)

	base := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: base, Raw: "line 1", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 2, Timestamp: base.Add(time.Minute), Raw: "line 2", TemplateID: "b", Template: "tpl b"},
		{LineNumber: 3, Timestamp: base.Add(2 * time.Minute), Raw: "line 3", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 4, Timestamp: base.Add(3 * time.Minute), Raw: "line 4", TemplateID: "c", Template: "tpl c"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	// Filter by template
	results, err := s.QueryLogs(QueryOpts{TemplateID: "a"})
	if err != nil {
		t.Fatalf("QueryLogs template filter: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("template filter: expected 2, got %d", len(results))
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

func TestTemplateSummaries(t *testing.T) {
	s := newTestStore(t)

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	entries := []LogEntry{
		{LineNumber: 1, Timestamp: ts, Raw: "line 1", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 2, Timestamp: ts, Raw: "line 2", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 3, Timestamp: ts, Raw: "line 3", TemplateID: "a", Template: "tpl a"},
		{LineNumber: 4, Timestamp: ts, Raw: "line 4", TemplateID: "b", Template: "tpl b"},
		{LineNumber: 5, Timestamp: ts, Raw: "line 5", TemplateID: "b", Template: "tpl b"},
		{LineNumber: 6, Timestamp: ts, Raw: "line 6", TemplateID: "c", Template: "tpl c"},
	}
	if err := s.InsertLogBatch(entries); err != nil {
		t.Fatalf("InsertLogBatch: %v", err)
	}

	summaries, err := s.TemplateSummaries()
	if err != nil {
		t.Fatalf("TemplateSummaries: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}

	// Ordered by count desc
	if summaries[0].TemplateID != "a" || summaries[0].Count != 3 {
		t.Errorf("first summary: got %+v, want template a with count 3", summaries[0])
	}
	if summaries[1].TemplateID != "b" || summaries[1].Count != 2 {
		t.Errorf("second summary: got %+v, want template b with count 2", summaries[1])
	}
	if summaries[2].TemplateID != "c" || summaries[2].Count != 1 {
		t.Errorf("third summary: got %+v, want template c with count 1", summaries[2])
	}
}
