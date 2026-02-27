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

	drainParser, err := parser.NewDrainParser()
	if err != nil {
		return errors.Errorf("drain parser: %w", err)
	}

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return errors.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		return errors.Errorf("store init: %w", err)
	}

	lines, err := collectAndStore(ctx, s, merged)
	if err != nil {
		return err
	}

	patterns, templateCount, err := discoverAndSavePatterns(ctx, s, drainParser, lines)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Ingested %d lines, discovered %d patterns (%d with 2+ matches)\n",
		len(lines), templateCount, patterns)
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "Run 'lapp label' to add semantic labels to patterns.\n")
	return nil
}

func collectAndStore(ctx context.Context, s *store.DuckDBStore, merged <-chan multiline.MergeResult) ([]string, error) {
	var lines []string
	var batch []store.LogEntry
	for rr := range merged {
		if rr.Err != nil {
			return nil, errors.Errorf("read log: %w", rr.Err)
		}
		ml := rr.Value
		lines = append(lines, ml.Content)
		batch = append(batch, store.LogEntry{
			LineNumber:    ml.StartLine,
			EndLineNumber: ml.EndLine,
			Timestamp:     time.Now(),
			Raw:           ml.Content,
		})

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				return nil, errors.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			return nil, errors.Errorf("insert batch: %w", err)
		}
	}
	return lines, nil
}

func discoverAndSavePatterns(ctx context.Context, s *store.DuckDBStore, dp *parser.DrainParser, lines []string) (patternCount, templateCount int, err error) {
	if err := dp.Feed(lines); err != nil {
		return 0, 0, errors.Errorf("drain feed: %w", err)
	}

	templates, err := dp.Templates()
	if err != nil {
		return 0, 0, errors.Errorf("drain templates: %w", err)
	}

	// Filter out single-match patterns (not generalized)
	var patterns []store.Pattern
	for _, t := range templates {
		if t.Count <= 1 {
			continue
		}
		patterns = append(patterns, store.Pattern{
			PatternUUIDString: t.ID.String(),
			PatternType:       "drain",
			RawPattern:        t.Pattern,
		})
	}
	if len(patterns) > 0 {
		if err := s.InsertPatterns(ctx, patterns); err != nil {
			return 0, 0, errors.Errorf("insert patterns: %w", err)
		}
	}

	return len(patterns), len(templates), nil
}
