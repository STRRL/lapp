package main

import (
	"fmt"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
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

func runTemplates(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return errors.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		return errors.Errorf("init store: %w", err)
	}

	summaries, err := s.PatternSummaries(ctx)
	if err != nil {
		return errors.Errorf("query: %w", err)
	}

	// Check if any summaries have semantic info
	hasLabels := false
	for _, ts := range summaries {
		if ts.SemanticID != "" {
			hasLabels = true
			break
		}
	}

	if hasLabels {
		fmt.Printf("%-12s %-6s %-22s %-6s %s\n", "ID", "TYPE", "SEMANTIC_ID", "COUNT", "DESCRIPTION")
		fmt.Println("------------ ------ ---------------------- ------ ----------------------------------------")
		for _, ts := range summaries {
			semanticID := ts.SemanticID
			if semanticID == "" {
				semanticID = "-"
			}
			desc := ts.Description
			if desc == "" {
				desc = "(not labeled)"
			}
			pType := ts.PatternType
			if pType == "" {
				pType = "-"
			}
			fmt.Printf("%-12s %-6s %-22s %-6d %s\n", ts.PatternID, pType, semanticID, ts.Count, desc)
		}
	} else {
		fmt.Printf("%-12s %-6s %-6s %s\n", "ID", "TYPE", "COUNT", "PATTERN")
		fmt.Println("------------ ------ ------ ----------------------------------------")
		for _, ts := range summaries {
			pType := ts.PatternType
			if pType == "" {
				pType = "-"
			}
			fmt.Printf("%-12s %-6s %-6d %s\n", ts.PatternID, pType, ts.Count, ts.Pattern)
		}
	}
	return nil
}
