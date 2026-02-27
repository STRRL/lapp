package multiline_test

import (
	"strings"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/ingestor"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/parser"
	"github.com/strrl/lapp/pkg/store"
)

func TestE2E_IngestMultilineToStore(t *testing.T) {
	ch, err := ingestor.Ingest("testdata/java_stacktrace.log")
	if err != nil {
		t.Fatal(err)
	}

	detector, err := multiline.NewDetector(multiline.DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	merged := multiline.Merge(ch, detector)

	grokParser, err := parser.NewGrokParser()
	if err != nil {
		t.Fatal(err)
	}
	chain := parser.NewChainParser(
		parser.NewJSONParser(),
		grokParser,
		parser.NewDrainParser(),
	)

	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}

	var batch []store.LogEntry
	for ml := range merged {
		result := chain.Parse(ml.Content)
		batch = append(batch, store.LogEntry{
			LineNumber:    ml.StartLine,
			EndLineNumber: ml.EndLine,
			Timestamp:     time.Now(),
			Raw:           ml.Content,
			TemplateID:    result.TemplateID,
			Template:      result.Template,
		})
	}

	if err := s.InsertLogBatch(batch); err != nil {
		t.Fatal(err)
	}

	// java_stacktrace.log has 13 physical lines but should produce 5 entries
	if len(batch) != 5 {
		for i, e := range batch {
			t.Logf("entry %d: lines %d-%d: %s",
				i, e.LineNumber, e.EndLineNumber, truncateStr(e.Raw, 80))
		}
		t.Fatalf("expected 5 stored entries, got %d", len(batch))
	}

	// Verify the stack trace entry spans multiple lines
	var foundStackTrace bool
	for _, e := range batch {
		if strings.Contains(e.Raw, "NullPointerException") &&
			strings.Contains(e.Raw, "Caused by:") {
			foundStackTrace = true
			if e.EndLineNumber <= e.LineNumber {
				t.Error("stack trace entry should span multiple lines")
			}
		}
	}
	if !foundStackTrace {
		t.Error("expected to find a stack trace entry with NullPointerException + Caused by")
	}

	// Verify roundtrip: query all entries back from store
	all, err := s.QueryLogs(store.QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(batch) {
		t.Fatalf("stored %d entries, queried back %d", len(batch), len(all))
	}

	// Verify templates were discovered
	summaries, err := s.TemplateSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) == 0 {
		t.Fatal("expected at least 1 template")
	}
	t.Logf("Stored %d entries, discovered %d templates", len(batch), len(summaries))
}

func TestE2E_SingleLineUnaffected(t *testing.T) {
	ch, err := ingestor.Ingest("testdata/single_line.log")
	if err != nil {
		t.Fatal(err)
	}

	detector, err := multiline.NewDetector(multiline.DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	merged := multiline.Merge(ch, detector)

	var count int
	for ml := range merged {
		if ml.StartLine != ml.EndLine {
			t.Errorf("entry %d: single-line log merged across lines %d-%d",
				count, ml.StartLine, ml.EndLine)
		}
		count++
	}

	// single_line.log has 5 lines, all should pass through as individual entries
	if count != 5 {
		t.Fatalf("expected 5 entries, got %d", count)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
