package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
)

func queryCmd() *cobra.Command {
	var templateID string

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query logs by template",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(templateID)
		},
	}
	cmd.Flags().StringVar(&templateID, "template", "", "template ID to filter by (required)")
	_ = cmd.MarkFlagRequired("template")
	return cmd
}

func runQuery(templateID string) error {
	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	q := querier.NewQuerier(s)
	entries, err := q.ByTemplate(templateID)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	for _, e := range entries {
		fmt.Printf("[%s] %s\n", e.TemplateID, e.Raw)
	}
	fmt.Fprintf(os.Stderr, "\n%d entries found\n", len(entries))
	return nil
}
