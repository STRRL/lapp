package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/querier"
	"github.com/strrl/lapp/pkg/store"
)

func templatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "List discovered templates",
		RunE:  runTemplates,
	}
	return cmd
}

func runTemplates(cmd *cobra.Command, args []string) error {
	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	q := querier.NewQuerier(s)
	summaries, err := q.Summary()
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	fmt.Printf("%-10s %-8s %s\n", "ID", "COUNT", "TEMPLATE")
	fmt.Println("---------- -------- ----------------------------------------")
	for _, ts := range summaries {
		fmt.Printf("%-10s %-8d %s\n", ts.TemplateID, ts.Count, ts.Template)
	}
	return nil
}
