package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
)

func queryCmd() *cobra.Command {
	var patternID string

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query logs by pattern",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(patternID)
		},
	}
	cmd.Flags().StringVar(&patternID, "pattern", "", "pattern ID to filter by (required)")
	_ = cmd.MarkFlagRequired("pattern")
	return cmd
}

func runQuery(patternID string) error {
	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	q := querier.NewQuerier(s)
	entries, err := q.ByPattern(patternID)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	for _, e := range entries {
		fmt.Printf("[%s] %s\n", e.PatternID, e.Raw)
	}
	fmt.Fprintf(os.Stderr, "\n%d entries found\n", len(entries))
	return nil
}
