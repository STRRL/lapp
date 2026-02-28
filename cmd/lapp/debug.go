package main

import (
	"log/slog"
	"os"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/analyzer"
	"github.com/strrl/lapp/pkg/analyzer/workspace"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
)

func debugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debugging utilities for development",
	}

	cmd.AddCommand(debugWorkspaceCmd())
	cmd.AddCommand(debugRunCmd())
	return cmd
}

var debugWorkspaceOutput string

func debugWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace <logfile>",
		Short: "Build an analysis workspace without running the AI agent",
		Long: `Parse the log file through the template pipeline and generate the workspace
files (raw.log, summary.txt, errors.txt) in a local directory for inspection.`,
		Args: cobra.ExactArgs(1),
		RunE: runDebugWorkspace,
	}
	cmd.Flags().StringVarP(&debugWorkspaceOutput, "output", "o", "", "output directory (default: auto-created in current dir)")
	return cmd
}

func runDebugWorkspace(cmd *cobra.Command, args []string) error {
	logFile := args[0]

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

	outDir := debugWorkspaceOutput
	if outDir == "" {
		outDir, err = os.MkdirTemp("", "lapp-workspace-*")
		if err != nil {
			return errors.Errorf("create output dir: %w", err)
		}
	} else {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return errors.Errorf("create output dir: %w", err)
		}
	}

	drainParser, err := pattern.NewDrainParser()
	if err != nil {
		return errors.Errorf("drain parser: %w", err)
	}

	slog.Info("Parsing entries", "count", len(mergedLines))
	if err := drainParser.Feed(mergedLines); err != nil {
		return errors.Errorf("drain feed: %w", err)
	}
	templates, err := drainParser.Templates()
	if err != nil {
		return errors.Errorf("drain templates: %w", err)
	}

	if err := workspace.NewBuilder(outDir, mergedLines, templates).BuildAll(); err != nil {
		return errors.Errorf("build workspace: %w", err)
	}

	slog.Info("Workspace created", "dir", outDir)
	return nil
}

var debugRunModel string

func debugRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <workspace-dir> [question]",
		Short: "Run the AI agent on an existing workspace directory",
		Long: `Run the AI agent analysis on a previously created workspace directory.

Requires OPENROUTER_API_KEY environment variable to be set.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runDebugRun,
	}
	cmd.Flags().StringVar(&debugRunModel, "model", "", "override LLM model (default: anthropic/claude-sonnet-4-6)")
	return cmd
}

func runDebugRun(cmd *cobra.Command, args []string) error {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

	workDir := args[0]
	var question string
	if len(args) > 1 {
		question = args[1]
	}

	// Verify workspace exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return errors.Errorf("workspace directory does not exist: %s", workDir)
	}

	config := analyzer.Config{
		APIKey: apiKey,
		Model:  debugRunModel,
	}

	slog.Info("Running agent on workspace", "dir", workDir)
	result, err := analyzer.RunAgent(cmd.Context(), config, workDir, question)
	if err != nil {
		return err
	}

	slog.Info(result)
	return nil
}
