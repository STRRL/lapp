package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/ingestor"
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
		return fmt.Errorf("ingest: %w", err)
	}

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
		return fmt.Errorf("drain parser: %w", err)
	}
	chain := parser.NewChainParser(
		drainParser,
	)

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		return fmt.Errorf("store init: %w", err)
	}

	count, err := ingestLines(ctx, s, ch, chain)
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

func ingestLines(ctx context.Context, s *store.DuckDBStore, ch <-chan ingestor.LogLine, chain *parser.ChainParser) (int, error) {
	var count int
	var batch []store.LogEntry
	for line := range ch {
		if line.Err != nil {
			return 0, fmt.Errorf("read log: %w", line.Err)
		}
		result := chain.Parse(line.Content)
		entry := store.LogEntry{
			LineNumber: line.LineNumber,
			Timestamp:  time.Now(),
			Raw:        line.Content,
			PatternID:  result.PatternID,
		}
		batch = append(batch, entry)

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				return 0, fmt.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
		count++
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			return 0, fmt.Errorf("insert batch: %w", err)
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
		return 0, 0, 0, fmt.Errorf("pattern counts: %w", err)
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
			return 0, 0, 0, fmt.Errorf("insert patterns: %w", err)
		}
	}

	cleared, err = s.ClearOrphanPatternIDs(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("clear orphan pattern IDs: %w", err)
	}

	return len(templates), len(patterns), cleared, nil
}
