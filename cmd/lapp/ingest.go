package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/ingestor"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/parser"
	"github.com/strrl/lapp/pkg/store"
)

func ingestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest <logfile>",
		Short: "Ingest a log file through the parser pipeline into the store",
		Long:  "Read a log file, parse each line through Drain, and store results in DuckDB.",
		Args:  cobra.ExactArgs(1),
		RunE:  runIngest,
	}
	return cmd
}

func runIngest(cmd *cobra.Command, args []string) error {
	logFile := args[0]

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	ch, err := ingestor.Ingest(ctx, logFile)
	if err != nil {
		return errors.Errorf("ingest: %w", err)
	}

	detector, err := multiline.NewDetector(multiline.DetectorConfig{})
	if err != nil {
		return errors.Errorf("multiline detector: %w", err)
	}
	merged := multiline.Merge(ch, detector)

	// Only use Drain for pattern discovery.
	// JSON/Grok parsers were removed because:
	// - Grok: predefined patterns (SYSLOG, APACHE) match structurally but don't
	//   produce generalized templates — a single pattern like "SYSLOG" covers all
	//   syslog lines without distinguishing between different log messages.
	// - JSON: similar issue — groups by key structure, not by message semantics.
	// - LLM: stub, not implemented yet.
	// Drain discovers meaningful patterns by clustering similar lines online.
	drainParser, err := parser.NewDrainParser()
	if err != nil {
		return errors.Errorf("drain parser: %w", err)
	}
	chain := parser.NewChainParser(
		drainParser,
	)

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return errors.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		return errors.Errorf("store init: %w", err)
	}

	count, err := ingestLines(ctx, s, merged, chain)
	if err != nil {
		return err
	}

	templates, patternCount, cleared, err := saveDiscoveredPatterns(ctx, s, chain)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Ingested %d lines, discovered %d patterns (%d with 2+ matches, %d orphan entries cleared)\n",
		count, templates, patternCount, cleared)
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "Run 'lapp label' to add semantic labels to patterns.\n")
	return nil
}

func ingestLines(ctx context.Context, s *store.DuckDBStore, merged <-chan multiline.MergeResult, chain *parser.ChainParser) (int, error) {
	var count int
	var batch []store.LogEntry
	for rr := range merged {
		if rr.Err != nil {
			return 0, errors.Errorf("read log: %w", rr.Err)
		}
		ml := rr.Value
		result := chain.Parse(ml.Content)
		entry := store.LogEntry{
			LineNumber:    ml.StartLine,
			EndLineNumber: ml.EndLine,
			Timestamp:     time.Now(),
			Raw:           ml.Content,
			PatternID:     result.PatternID,
		}
		batch = append(batch, entry)

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				return 0, errors.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
		count++
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			return 0, errors.Errorf("insert batch: %w", err)
		}
	}
	return count, nil
}

func saveDiscoveredPatterns(ctx context.Context, s *store.DuckDBStore, chain *parser.ChainParser) (templateCount, patternCount int, cleared int64, err error) {
	templates := chain.Templates()

	// Count occurrences per pattern to filter out single-match patterns.
	// Drain is an online algorithm — a cluster with only 1 log line means
	// Drain never saw a similar line, so the "pattern" is just the literal
	// original text with no generalization. Not useful as a pattern.
	patternCounts, err := s.PatternCounts(ctx)
	if err != nil {
		return 0, 0, 0, errors.Errorf("pattern counts: %w", err)
	}

	var patterns []store.Pattern
	for _, t := range templates {
		if patternCounts[t.ID] <= 1 {
			continue
		}
		patterns = append(patterns, store.Pattern{
			PatternID:   t.ID,
			PatternType: "drain",
			RawPattern:  t.Pattern,
		})
	}
	if len(patterns) > 0 {
		if err := s.InsertPatterns(ctx, patterns); err != nil {
			return 0, 0, 0, errors.Errorf("insert patterns: %w", err)
		}
	}

	cleared, err = s.ClearOrphanPatternIDs(ctx)
	if err != nil {
		return 0, 0, 0, errors.Errorf("clear orphan pattern IDs: %w", err)
	}

	return len(templates), len(patterns), cleared, nil
}
