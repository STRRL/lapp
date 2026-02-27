package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/analyzer"
	"github.com/strrl/lapp/pkg/parser"
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

	fmt.Fprintf(os.Stderr, "Reading logs...\n")
	lines, err := readLines(logFile)
	if err != nil {
		return fmt.Errorf("read log file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Read %d lines\n", len(lines))

	outDir := debugWorkspaceOutput
	if outDir == "" {
		outDir, err = os.MkdirTemp("", "lapp-workspace-*")
		if err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	} else {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}

	chain, err := buildParserChain()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Parsing %d lines...\n", len(lines))
	if err := analyzer.BuildWorkspace(outDir, lines, chain); err != nil {
		return fmt.Errorf("build workspace: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Workspace created at: %s\n", outDir)
	fmt.Println(outDir)
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
		return fmt.Errorf("OPENROUTER_API_KEY environment variable is required")
	}

	workDir := args[0]
	var question string
	if len(args) > 1 {
		question = args[1]
	}

	// Verify workspace exists
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return fmt.Errorf("workspace directory does not exist: %s", workDir)
	}

	config := analyzer.Config{
		APIKey: apiKey,
		Model:  debugRunModel,
	}

	fmt.Fprintf(os.Stderr, "Running agent on workspace: %s\n", workDir)
	result, err := analyzer.RunAgent(cmd.Context(), config, workDir, question)
	if err != nil {
		return err
	}

	fmt.Println(result)
	return nil
}

func buildParserChain() (*parser.ChainParser, error) {
	grokParser, err := parser.NewGrokParser()
	if err != nil {
		return nil, fmt.Errorf("grok parser: %w", err)
	}
	return parser.NewChainParser(
		parser.NewJSONParser(),
		grokParser,
		parser.NewDrainParser(),
	), nil
}
