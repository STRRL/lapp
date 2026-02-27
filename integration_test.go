package lapp_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/parser"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
	"github.com/strrl/lapp/pkg/test/loghub"
)

func TestIntegrationHDFS(t *testing.T) {
	loghubPath := os.Getenv("LOGHUB_PATH")
	if loghubPath == "" {
		t.Skip("LOGHUB_PATH not set, skipping integration test")
	}

	csvPath := filepath.Join(loghubPath, "HDFS", "HDFS_2k.log_structured_corrected.csv")

	entries, err := loghub.LoadDataset(csvPath)
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	t.Logf("Loaded %d log entries from HDFS dataset", len(entries))

	grokParser, err := parser.NewGrokParser()
	if err != nil {
		t.Fatalf("create grok parser: %v", err)
	}
	chain := parser.NewChainParser(
		parser.NewJSONParser(),
		grokParser,
		parser.NewDrainParser(),
		parser.NewLLMParser(),
	)

	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	var batch []store.LogEntry
	for i, entry := range entries {
		result := chain.Parse(entry.Content)
		logEntry := store.LogEntry{
			LineNumber: i + 1,
			Timestamp:  time.Now(),
			Raw:        entry.Content,
			TemplateID: result.TemplateID,
			Template:   result.Template,
		}
		batch = append(batch, logEntry)
	}

	if err := s.InsertLogBatch(batch); err != nil {
		t.Fatalf("insert batch: %v", err)
	}

	q := querier.NewQuerier(s)

	summaries, err := q.Summary()
	if err != nil {
		t.Fatalf("get summaries: %v", err)
	}

	t.Logf("Discovered %d templates", len(summaries))
	for i, ts := range summaries {
		if i >= 10 {
			t.Logf("  ... and %d more templates", len(summaries)-10)
			break
		}
		t.Logf("  [%s] count=%d pattern=%q", ts.TemplateID, ts.Count, ts.Template)
	}

	if len(summaries) == 0 {
		t.Fatal("expected at least one template to be discovered")
	}
	if len(summaries) >= len(entries) {
		t.Fatalf("expected fewer templates (%d) than entries (%d)", len(summaries), len(entries))
	}

	first := summaries[0]
	matched, err := q.ByTemplate(first.TemplateID)
	if err != nil {
		t.Fatalf("query by template: %v", err)
	}
	if len(matched) == 0 {
		t.Fatalf("expected entries for template %s, got none", first.TemplateID)
	}
	t.Logf("Query by template %s returned %d entries", first.TemplateID, len(matched))
}
