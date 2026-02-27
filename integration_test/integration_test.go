package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/ingestor"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
	"github.com/strrl/lapp/pkg/test/loghub"
)

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

			chain := newChainParser(t)
			s := newStore(t)

			var batch []store.LogEntry
			for i, entry := range entries {
				result := chain.Parse(entry.Content)
				batch = append(batch, store.LogEntry{
					LineNumber: i + 1,
					Timestamp:  time.Now(),
					Raw:        entry.Content,
					PatternID:  result.PatternID,
				})
			}

			if err := s.InsertLogBatch(ctx, batch); err != nil {
				t.Fatalf("insert batch: %v", err)
			}

			q := querier.NewQuerier(s)
			summaries, err := q.Summary(ctx)
			if err != nil {
				t.Fatalf("get summaries: %v", err)
			}

			t.Logf("Discovered %d templates from %d entries", len(summaries), len(entries))

			// Save discovered templates
			tplSummaries := make([]templateSummary, len(summaries))
			for i, s := range summaries {
				tplSummaries[i] = templateSummary{
					PatternID: s.PatternID,
					Pattern:   s.Pattern,
					Count:     s.Count,
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

			// Verify query-by-template roundtrip
			first := summaries[0]
			matched, err := q.ByPattern(ctx, first.PatternID)
			if err != nil {
				t.Fatalf("query by template: %v", err)
			}
			if len(matched) == 0 {
				t.Fatalf("expected entries for template %s, got none", first.PatternID)
			}
			if len(matched) < first.Count {
				t.Fatalf("query returned %d entries, expected at least %d", len(matched), first.Count)
			}
		})
	}
}

// TestAllDatasets_IngestorPath reads each raw .log file via the ingestor,
// parses via the chain, stores, and verifies the full end-to-end pipeline.
func TestAllDatasets_IngestorPath(t *testing.T) {
	basePath := loghubPath(t)
	outDir := outputDir(t)

	for _, ds := range datasets {
		t.Run(ds, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			logPath := filepath.Join(basePath, ds, ds+"_2k.log")
			ch, err := ingestor.Ingest(ctx, logPath)
			if err != nil {
				t.Fatalf("ingest: %v", err)
			}

			chain := newChainParser(t)
			s := newStore(t)

			var batch []store.LogEntry
			for line := range ch {
				if line.Err != nil {
					t.Fatalf("ingest read error: %v", line.Err)
				}
				result := chain.Parse(line.Content)
				batch = append(batch, store.LogEntry{
					LineNumber: line.LineNumber,
					Timestamp:  time.Now(),
					Raw:        line.Content,
					PatternID:  result.PatternID,
				})
			}

			if len(batch) == 0 {
				t.Fatal("expected at least 1 ingested line, got 0")
			}

			if err := s.InsertLogBatch(ctx, batch); err != nil {
				t.Fatalf("insert batch: %v", err)
			}
			t.Logf("Ingested and stored %d lines", len(batch))

			q := querier.NewQuerier(s)
			summaries, err := q.Summary(ctx)
			if err != nil {
				t.Fatalf("get summaries: %v", err)
			}

			t.Logf("Discovered %d templates from %d entries", len(summaries), len(batch))

			// Save discovered templates
			tplSummaries := make([]templateSummary, len(summaries))
			for i, s := range summaries {
				tplSummaries[i] = templateSummary{
					PatternID: s.PatternID,
					Pattern:   s.Pattern,
					Count:     s.Count,
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
			allEntries, err := q.Search(ctx, store.QueryOpts{})
			if err != nil {
				t.Fatalf("search all: %v", err)
			}
			if len(allEntries) != len(batch) {
				t.Fatalf("stored %d entries, expected %d", len(allEntries), len(batch))
			}

			// Verify query-by-template roundtrip
			first := summaries[0]
			matched, err := q.ByPattern(ctx, first.PatternID)
			if err != nil {
				t.Fatalf("query by template: %v", err)
			}
			if len(matched) == 0 {
				t.Fatalf("expected entries for template %s, got none", first.PatternID)
			}
		})
	}
}
