package main

import (
	"context"
	"fmt"
	"os"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
)

func queryCmd() *cobra.Command {
	var patternID string

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query logs by pattern",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runQuery(cmd.Context(), patternID)
		},
	}
	cmd.Flags().StringVar(&patternID, "pattern", "", "pattern ID to filter by (required)")
	_ = cmd.MarkFlagRequired("pattern")
	return cmd
}

func runQuery(ctx context.Context, patternID string) error {
	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return errors.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	q := querier.NewQuerier(s)
	entries, err := q.ByPattern(ctx, patternID)
	if err != nil {
		return errors.Errorf("query: %w", err)
	}

	for _, e := range entries {
		fmt.Printf("[%s] %s\n", e.PatternID, e.Raw)
	}
	fmt.Fprintf(os.Stderr, "\n%d entries found\n", len(entries))
	return nil
}
