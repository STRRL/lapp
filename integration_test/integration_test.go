package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/strrl/lapp/integration_test/loghub"
	"github.com/strrl/lapp/pkg/logsource"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/store"
)

func TestMain(m *testing.M) {
	// Load .env.test if present (does not override existing env vars)
	_ = godotenv.Load("../.env.test")
	os.Exit(m.Run())
}

var datasets = []string{
	"Apache",
	"BGL",
	"Hadoop",
	"HDFS",
	"HealthApp",
	"HPC",
	"Linux",
	"Mac",
	"OpenSSH",
	"OpenStack",
	"Proxifier",
	"Spark",
	"Thunderbird",
	"Zookeeper",
}

// TestAllDatasets_CSVPath loads each dataset from the corrected CSV,
// parses all entries, stores them, and verifies template discovery and querying.
func TestAllDatasets_CSVPath(t *testing.T) {
	basePath := loghubPath(t)
	outDir := outputDir(t)

	for _, ds := range datasets {
		t.Run(ds, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			csvPath := filepath.Join(basePath, ds, ds+"_2k.log_structured_corrected.csv")
			entries, err := loghub.LoadDataset(csvPath)
			if err != nil {
				t.Fatalf("load dataset: %v", err)
			}
			if len(entries) == 0 {
				t.Fatal("expected at least 1 entry, got 0")
			}
			t.Logf("Loaded %d entries", len(entries))

			dp := newDrainParser(t)
			s := newStore(t)

			// Collect lines
			lines := make([]string, len(entries))
			for i, entry := range entries {
				lines[i] = entry.Content
			}

			// Feed all lines and get templates
			if err := dp.Feed(lines); err != nil {
				t.Fatalf("feed: %v", err)
			}
			templates, err := dp.Templates()
			if err != nil {
				t.Fatalf("templates: %v", err)
			}

			// Store log entries with labels assigned via MatchTemplate
			batch := make([]store.LogEntry, len(entries))
			for i, entry := range entries {
				le := store.LogEntry{
					LineNumber: i + 1,
					Timestamp:  time.Now(),
					Raw:        entry.Content,
				}
				if tpl, ok := pattern.MatchTemplate(entry.Content, templates); ok {
					le.Labels = map[string]string{"pattern": tpl.ID.String(), "pattern_id": tpl.ID.String()}
				}
				batch[i] = le
			}
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				t.Fatalf("insert batch: %v", err)
			}

			// Insert discovered patterns into the patterns table
			patterns := make([]store.Pattern, len(templates))
			for i, tpl := range templates {
				patterns[i] = store.Pattern{
					PatternUUIDString: tpl.ID.String(),
					RawPattern:        tpl.Pattern,
					SemanticID:        tpl.ID.String(),
				}
			}
			if err := s.InsertPatterns(ctx, patterns); err != nil {
				t.Fatalf("insert patterns: %v", err)
			}

			summaries, err := s.PatternSummaries(ctx)
			if err != nil {
				t.Fatalf("get summaries: %v", err)
			}

			t.Logf("Discovered %d templates from %d entries", len(summaries), len(entries))

			// Save discovered templates
			tplSummaries := make([]templateSummary, len(summaries))
			for i, sm := range summaries {
				tplSummaries[i] = templateSummary{
					PatternUUIDString: sm.PatternUUIDString,
					Pattern:           sm.Pattern,
					Count:             sm.Count,
				}
			}
			saveTemplates(t, outDir, templateResult{
				Dataset:       ds,
				TestPath:      "csv",
				TotalEntries:  len(entries),
				TemplateCount: len(summaries),
				Templates:     tplSummaries,
			})

			if len(summaries) == 0 {
				t.Fatal("expected at least 1 template")
			}
			if len(summaries) >= len(entries) {
				t.Fatalf("expected fewer templates (%d) than entries (%d)", len(summaries), len(entries))
			}
		})
	}
}

// TestAllDatasets_IngestorPath reads each raw .log file via the ingestor,
// parses via Drain, stores, and verifies the full end-to-end pipeline.
func TestAllDatasets_IngestorPath(t *testing.T) {
	basePath := loghubPath(t)
	outDir := outputDir(t)

	for _, ds := range datasets {
		t.Run(ds, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			logPath := filepath.Join(basePath, ds, ds+"_2k.log")
			ch, err := logsource.Ingest(ctx, logPath)
			if err != nil {
				t.Fatalf("ingest: %v", err)
			}

			dp := newDrainParser(t)
			s := newStore(t)

			// Collect lines from ingestor
			type logLine struct {
				lineNumber int
				content    string
			}
			var collected []logLine
			for rr := range ch {
				if rr.Err != nil {
					t.Fatalf("ingest read error: %v", rr.Err)
				}
				collected = append(collected, logLine{
					lineNumber: rr.Value.LineNumber,
					content:    rr.Value.Content,
				})
			}

			if len(collected) == 0 {
				t.Fatal("expected at least 1 ingested line, got 0")
			}

			// Feed all lines and get templates
			lines := make([]string, len(collected))
			for i, ll := range collected {
				lines[i] = ll.content
			}
			if err := dp.Feed(lines); err != nil {
				t.Fatalf("feed: %v", err)
			}
			templates, err := dp.Templates()
			if err != nil {
				t.Fatalf("templates: %v", err)
			}

			// Store log entries with labels assigned via MatchTemplate
			batch := make([]store.LogEntry, len(collected))
			for i, ll := range collected {
				le := store.LogEntry{
					LineNumber: ll.lineNumber,
					Timestamp:  time.Now(),
					Raw:        ll.content,
				}
				if tpl, ok := pattern.MatchTemplate(ll.content, templates); ok {
					le.Labels = map[string]string{"pattern": tpl.ID.String(), "pattern_id": tpl.ID.String()}
				}
				batch[i] = le
			}
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				t.Fatalf("insert batch: %v", err)
			}
			t.Logf("Ingested and stored %d lines", len(batch))

			// Insert discovered patterns into the patterns table
			patterns := make([]store.Pattern, len(templates))
			for i, tpl := range templates {
				patterns[i] = store.Pattern{
					PatternUUIDString: tpl.ID.String(),
					RawPattern:        tpl.Pattern,
					SemanticID:        tpl.ID.String(),
				}
			}
			if err := s.InsertPatterns(ctx, patterns); err != nil {
				t.Fatalf("insert patterns: %v", err)
			}

			summaries, err := s.PatternSummaries(ctx)
			if err != nil {
				t.Fatalf("get summaries: %v", err)
			}

			t.Logf("Discovered %d templates from %d entries", len(summaries), len(batch))

			// Save discovered templates
			tplSummaries := make([]templateSummary, len(summaries))
			for i, sm := range summaries {
				tplSummaries[i] = templateSummary{
					PatternUUIDString: sm.PatternUUIDString,
					Pattern:           sm.Pattern,
					Count:             sm.Count,
				}
			}
			saveTemplates(t, outDir, templateResult{
				Dataset:       ds,
				TestPath:      "ingestor",
				TotalEntries:  len(batch),
				TemplateCount: len(summaries),
				Templates:     tplSummaries,
			})

			if len(summaries) == 0 {
				t.Fatal("expected at least 1 template")
			}
			if len(summaries) >= len(batch) {
				t.Fatalf("expected fewer templates (%d) than entries (%d)", len(summaries), len(batch))
			}

			// Verify total stored entries match ingested count
			allEntries, err := s.QueryLogs(ctx, store.QueryOpts{})
			if err != nil {
				t.Fatalf("search all: %v", err)
			}
			if len(allEntries) != len(batch) {
				t.Fatalf("stored %d entries, expected %d", len(allEntries), len(batch))
			}
		})
	}
}
