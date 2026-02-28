package main

import (
	"log/slog"

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
			slog.Info("template",
				"id", ts.PatternUUIDString,
				"type", pType,
				"semantic_id", semanticID,
				"count", ts.Count,
				"description", desc,
			)
		}
	} else {
		for _, ts := range summaries {
			pType := ts.PatternType
			if pType == "" {
				pType = "-"
			}
			slog.Info("template",
				"id", ts.PatternUUIDString,
				"type", pType,
				"count", ts.Count,
				"pattern", ts.Pattern,
			)
		}
	}
	return nil
}
