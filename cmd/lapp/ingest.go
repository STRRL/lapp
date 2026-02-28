package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/logsource"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/store"
)

func debugIngestCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "ingest <logfile>",
		Short: "Run the ingest pipeline only (Drain + semantic labeling + DuckDB)",
		Long:  "Read a log file, parse each line through Drain, store results in DuckDB, and label patterns with semantic IDs via LLM.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDebugIngest(cmd, args, model)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "LLM model to use for labeling (default: $MODEL_NAME or google/gemini-3-flash-preview)")
	return cmd
}

func runDebugIngest(cmd *cobra.Command, args []string, model string) error {
	logFile := args[0]

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

	ch, err := logsource.Ingest(ctx, logFile)
	if err != nil {
		return errors.Errorf("ingest: %w", err)
	}

	detector, err := multiline.NewDetector(multiline.DetectorConfig{})
	if err != nil {
		return errors.Errorf("multiline detector: %w", err)
	}
	merged := multiline.Merge(ch, detector)

	drainParser, err := pattern.NewDrainParser()
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

	slog.Info("Ingestion complete",
		"lines", len(lines),
		"templates", templateCount,
		"patterns_with_2+_matches", patternCount,
	)
	slog.Info("Database stored", "path", dbPath)
	return nil
}
