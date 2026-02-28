package main

import (
	"bufio"
	"log/slog"
	"os"
	"strings"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/analyzer"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/store"
)

var analyzeModel string

func analyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <logfile> [question]",
		Short: "Analyze a log file using an AI agent to find root causes",
		Long: `Read a log file, run the full ingest pipeline (Drain clustering, semantic labeling,
DuckDB storage), then use an AI agent to autonomously explore the processed logs
and provide analysis.

Requires OPENROUTER_API_KEY environment variable to be set.

Examples:
  lapp analyze app.log
  lapp analyze app.log "why is my service returning 502?"
  lapp analyze app.log "what caused the crash?"`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runAnalyze,
	}
	cmd.Flags().StringVar(&analyzeModel, "model", "", "override LLM model (default: anthropic/claude-sonnet-4-6)")
	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.New("OPENROUTER_API_KEY environment variable is required")
	}

	logFile := args[0]
	var question string
	if len(args) > 1 {
		question = args[1]
	}

	ctx := cmd.Context()

	// Read all lines
	slog.Info("Reading logs...")
	lines, err := readLines(logFile)
	if err != nil {
		return errors.Errorf("read log file: %w", err)
	}
	detector, err := multiline.NewDetector(multiline.DetectorConfig{})
	if err != nil {
		return errors.Errorf("multiline detector: %w", err)
	}
	merged := multiline.MergeSlice(lines, detector)
	mergedLines := make([]string, len(merged))
	for i, m := range merged {
		mergedLines[i] = m.Content
	}
	slog.Info("Read lines", "lines", len(lines), "merged_entries", len(mergedLines))

	// Ingest pipeline: Drain clustering + semantic labeling + DuckDB storage
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

	semanticIDMap, patternCount, templateCount, err := discoverAndSavePatterns(ctx, s, drainParser, mergedLines, semantic.Config{
		APIKey: apiKey,
		Model:  analyzeModel,
	})
	if err != nil {
		return err
	}

	templates, err := drainParser.Templates()
	if err != nil {
		return errors.Errorf("drain templates: %w", err)
	}
	if err := storeLogsWithLabels(ctx, s, merged, templates, semanticIDMap); err != nil {
		return err
	}

	slog.Info("Ingestion complete",
		"lines", len(mergedLines),
		"templates", templateCount,
		"patterns_with_2+_matches", patternCount,
	)
	slog.Info("Database stored", "path", dbPath)

	// Run AI agent analysis
	config := analyzer.Config{
		APIKey: apiKey,
		Model:  analyzeModel,
	}

	result, err := analyzer.Analyze(ctx, config, mergedLines, question)
	if err != nil {
		return err
	}

	slog.Info(result)
	return nil
}

func readLines(path string) ([]string, error) {
	reader, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	var lines []string
	scanner := bufio.NewScanner(reader)
	// Increase buffer for long lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Trim trailing empty lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}
