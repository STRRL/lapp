package main

import (
	"context"
	"fmt"
	"os"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/labeler"
	"github.com/strrl/lapp/pkg/store"
)

func labelCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Add semantic labels to discovered patterns using an LLM",
		Long:  "Query the patterns table and use an LLM to generate semantic IDs and descriptions for each pattern.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLabel(cmd, model)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "LLM model to use (default: $MODEL_NAME or google/gemini-3-flash-preview)")
	return cmd
}

func runLabel(cmd *cobra.Command, model string) error {
	ctx := cmd.Context()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return errors.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		return errors.Errorf("store init: %w", err)
	}

	patterns, err := s.Patterns(ctx)
	if err != nil {
		return errors.Errorf("query patterns: %w", err)
	}

	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "No patterns found. Run 'lapp ingest' first.")
		return nil
	}

	// Build pattern inputs with sample lines
	var inputs []labeler.PatternInput
	for _, p := range patterns {
		samples, err := sampleLines(ctx, s, p.PatternID, 3)
		if err != nil {
			return errors.Errorf("sample lines for %s: %w", p.PatternID, err)
		}
		inputs = append(inputs, labeler.PatternInput{
			PatternID: p.PatternID,
			Pattern:   p.RawPattern,
			Samples:   samples,
		})
	}

	fmt.Fprintf(os.Stderr, "Labeling %d patterns...\n", len(inputs))

	labels, err := labeler.Label(ctx, labeler.Config{
		APIKey: apiKey,
		Model:  model,
	}, inputs)
	if err != nil {
		return errors.Errorf("label: %w", err)
	}

	// Convert to store.Pattern for update
	var updates []store.Pattern
	for _, l := range labels {
		updates = append(updates, store.Pattern{
			PatternID:   l.PatternID,
			SemanticID:  l.SemanticID,
			Description: l.Description,
		})
	}

	if len(updates) == 0 {
		fmt.Fprintln(os.Stderr, "No labels returned by LLM.")
		return nil
	}

	if err := s.UpdatePatternLabels(ctx, updates); err != nil {
		return errors.Errorf("update labels: %w", err)
	}

	// Print results
	fmt.Printf("%-12s %-25s %s\n", "ID", "SEMANTIC_ID", "DESCRIPTION")
	fmt.Println("------------ ------------------------- ----------------------------------------")
	for _, l := range labels {
		fmt.Printf("%-12s %-25s %s\n", l.PatternID, l.SemanticID, l.Description)
	}

	return nil
}

func sampleLines(ctx context.Context, s store.Store, patternID string, n int) ([]string, error) {
	entries, err := s.QueryLogs(ctx, store.QueryOpts{
		PatternID: patternID,
		Limit:     n,
	})
	if err != nil {
		return nil, errors.Errorf("query logs: %w", err)
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, e.Raw)
	}
	return lines, nil
}
