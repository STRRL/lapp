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
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/store"
)

func ingestCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "ingest <logfile>",
		Short: "Ingest a log file through the parser pipeline into the store",
		Long:  "Read a log file, parse each line through Drain, store results in DuckDB, and label patterns with semantic IDs via LLM.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(cmd, args, model)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "LLM model to use for labeling (default: $MODEL_NAME or google/gemini-3-flash-preview)")
	return cmd
}

func runIngest(cmd *cobra.Command, args []string, model string) error {
	logFile := args[0]

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

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

	// Round 1: Collect all lines in memory (no DB writes yet)
	mergedLines, err := collectLines(merged)
	if err != nil {
		return err
	}

	var lines []string
	for _, ml := range mergedLines {
		lines = append(lines, ml.Content)
	}

	// Discover patterns, label them, and save to patterns table
	semanticIDMap, patternCount, templateCount, err := discoverAndSavePatterns(ctx, s, drainParser, lines, semantic.Config{
		APIKey: apiKey,
		Model:  model,
	})
	if err != nil {
		return err
	}

	// Round 2: Match each line to a pattern and store with labels
	templates, err := drainParser.Templates()
	if err != nil {
		return errors.Errorf("drain templates: %w", err)
	}
	err = storeLogsWithLabels(ctx, s, mergedLines, templates, semanticIDMap)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Ingested %d lines, discovered %d patterns (%d with 2+ matches)\n",
		len(lines), templateCount, patternCount)
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	return nil
}

func collectLines(merged <-chan multiline.MergeResult) ([]multiline.MergedLine, error) {
	var lines []multiline.MergedLine
	for rr := range merged {
		if rr.Err != nil {
			return nil, errors.Errorf("read log: %w", rr.Err)
		}
		lines = append(lines, *rr.Value)
	}
	return lines, nil
}

func discoverAndSavePatterns(
	ctx context.Context,
	s *store.DuckDBStore,
	dp *parser.DrainParser,
	lines []string,
	labelCfg semantic.Config,
) (semanticIDMap map[string]string, patternCount, templateCount int, err error) {
	if err := dp.Feed(lines); err != nil {
		return nil, 0, 0, errors.Errorf("drain feed: %w", err)
	}

	templates, err := dp.Templates()
	if err != nil {
		return nil, 0, 0, errors.Errorf("drain templates: %w", err)
	}

	// Filter out single-match patterns (not generalized)
	var filtered []parser.DrainCluster
	for _, t := range templates {
		if t.Count <= 1 {
			continue
		}
		filtered = append(filtered, t)
	}

	semanticIDMap = make(map[string]string)

	if len(filtered) == 0 {
		return semanticIDMap, 0, len(templates), nil
	}

	// Build labeler inputs with sample lines from in-memory data
	inputs := buildLabelInputs(filtered, lines)

	fmt.Fprintf(os.Stderr, "Labeling %d patterns...\n", len(inputs))

	labels, err := semantic.Label(ctx, labelCfg, inputs)
	if err != nil {
		return nil, 0, 0, errors.Errorf("label: %w", err)
	}

	// Index labels by pattern UUID for lookup
	labelMap := make(map[string]semantic.SemanticLabel, len(labels))
	for _, l := range labels {
		labelMap[l.PatternUUIDString] = l
	}

	// Build store patterns with semantic labels and populate semanticIDMap
	var patterns []store.Pattern
	for _, t := range filtered {
		p := store.Pattern{
			PatternUUIDString: t.ID.String(),
			PatternType:       "drain",
			RawPattern:        t.Pattern,
		}
		if l, ok := labelMap[t.ID.String()]; ok {
			p.SemanticID = l.SemanticID
			p.Description = l.Description
			semanticIDMap[t.ID.String()] = l.SemanticID
		}
		patterns = append(patterns, p)
	}

	if err := s.InsertPatterns(ctx, patterns); err != nil {
		return nil, 0, 0, errors.Errorf("insert patterns: %w", err)
	}

	return semanticIDMap, len(patterns), len(templates), nil
}

func storeLogsWithLabels(
	ctx context.Context,
	s *store.DuckDBStore,
	mergedLines []multiline.MergedLine,
	templates []parser.DrainCluster,
	semanticIDMap map[string]string,
) error {
	var batch []store.LogEntry
	for _, ml := range mergedLines {
		entry := store.LogEntry{
			LineNumber:    ml.StartLine,
			EndLineNumber: ml.EndLine,
			Timestamp:     time.Now(),
			Raw:           ml.Content,
		}

		if tpl, ok := parser.MatchTemplate(ml.Content, templates); ok {
			if sid, found := semanticIDMap[tpl.ID.String()]; found {
				entry.Labels = map[string]string{
					"pattern":    sid,
					"pattern_id": tpl.ID.String(),
				}
			}
		}

		batch = append(batch, entry)

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				return errors.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			return errors.Errorf("insert batch: %w", err)
		}
	}
	return nil
}

func buildLabelInputs(templates []parser.DrainCluster, lines []string) []semantic.PatternInput {
	var inputs []semantic.PatternInput
	for _, t := range templates {
		var samples []string
		for _, line := range lines {
			if _, ok := parser.MatchTemplate(line, []parser.DrainCluster{t}); ok {
				samples = append(samples, line)
				if len(samples) >= 3 {
					break
				}
			}
		}
		inputs = append(inputs, semantic.PatternInput{
			PatternUUIDString: t.ID.String(),
			Pattern:           t.Pattern,
			Samples:           samples,
		})
	}
	return inputs
}
