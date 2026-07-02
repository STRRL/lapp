package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/analyzer"
	"github.com/strrl/lapp/pkg/gcplog"
	"github.com/strrl/lapp/pkg/workspace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// topicToDir sanitizes a topic string to lower-kebab-case and returns
// the workspace path under ~/.lapp/workspaces/<sanitized-topic>.
func topicToDir(topic string) (string, error) {
	slug := strings.ToLower(topic)
	slug = nonAlphaNum.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "", errors.New("topic results in empty name after sanitization")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".lapp", "workspaces", slug), nil
}

func workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Create and manage structured log investigation workspaces",
	}
	cmd.AddCommand(workspaceCreateCmd())
	cmd.AddCommand(workspaceListCmd())
	cmd.AddCommand(workspaceAddLogCmd())
	cmd.AddCommand(workspaceAnalyzeCmd())
	return cmd
}

func workspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all workspaces",
		Args:  cobra.NoArgs,
		RunE:  runWorkspaceList,
	}
}

func runWorkspaceList(_ *cobra.Command, _ []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.Errorf("resolve home dir: %w", err)
	}
	wsDir := filepath.Join(home, ".lapp", "workspaces")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No workspaces found.")
			return nil
		}
		return errors.Errorf("read workspaces dir: %w", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			fmt.Println(e.Name())
			found = true
		}
	}
	if !found {
		fmt.Println("No workspaces found.")
	}
	return nil
}

// availableWorkspacesHint returns a hint string listing existing workspaces,
// or an empty string if none exist.
func availableWorkspacesHint() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	wsDir := filepath.Join(home, ".lapp", "workspaces")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "\navailable workspaces: " + strings.Join(names, ", ")
}

func workspaceCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <topic>",
		Short: "Create a new workspace for the given topic",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkspaceCreate,
	}
}

func runWorkspaceCreate(_ *cobra.Command, args []string) error {
	dir, err := topicToDir(args[0])
	if err != nil {
		return err
	}

	for _, sub := range []string{"logs", workspace.DiscoveryRunsDirName} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return errors.Errorf("create %s: %w", sub, err)
		}
	}

	topic := filepath.Base(dir)
	agentsMD := `# Log Investigation Workspace

This workspace has been created but no log files have been added yet.

Use ` + "`lapp workspace add-log --topic " + topic + " <logfile>`" + ` to add log files.
`
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		return errors.Errorf("write AGENTS.md: %w", err)
	}

	slog.Info("Workspace created", "dir", dir)
	return nil
}

var addLogModel string
var addLogStdin bool
var addLogTopic string
var addLogGCPProject string
var addLogGCPFilter string
var addLogGCPSince time.Duration
var addLogGCPLimit int

func workspaceAddLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-log [logfile]",
		Short: "Add a log file to the workspace and start discovery",
		Long: `Copy a log file into the workspace's logs/ directory, then run the full
DiscoveryRun flow (Drain clustering + semantic labeling) to generate run-scoped results.

Logs can come from a file, stdin, or Google Cloud Logging (--gcp-project,
requires Application Default Credentials).

Requires OPENROUTER_API_KEY environment variable.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runWorkspaceAddLog,
	}
	cmd.Flags().StringVar(&addLogTopic, "topic", "", "workspace topic (required)")
	cmd.Flags().StringVar(&addLogModel, "model", "", "override LLM model")
	cmd.Flags().BoolVar(&addLogStdin, "stdin", false, "read log from stdin")
	cmd.Flags().StringVar(&addLogGCPProject, "gcp-project", "", "import logs from this Google Cloud project")
	cmd.Flags().StringVar(&addLogGCPFilter, "gcp-filter", "", "Cloud Logging filter expression")
	cmd.Flags().DurationVar(&addLogGCPSince, "since", time.Hour, "how far back to fetch Cloud Logging entries")
	cmd.Flags().IntVar(&addLogGCPLimit, "limit", 10000, "maximum number of Cloud Logging entries to fetch")
	_ = cmd.MarkFlagRequired("topic")
	return cmd
}

func runWorkspaceAddLog(cmd *cobra.Command, args []string) error {
	dir, err := topicToDir(addLogTopic)
	if err != nil {
		return err
	}

	// Validate workspace exists
	if _, err := os.Stat(filepath.Join(dir, "logs")); os.IsNotExist(err) {
		hint := availableWorkspacesHint()
		return errors.Errorf("not a workspace: %s (no logs/ directory)%s", dir, hint)
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return errors.New("OPENROUTER_API_KEY environment variable is required")
	}

	ctx, span := otel.Tracer("lapp/cmd").Start(cmd.Context(), "cmd.WorkspaceAddLog")
	defer span.End()

	if err := copyLogToWorkspace(ctx, dir, args, span.SetAttributes); err != nil {
		return err
	}

	result, err := workspace.Discover(ctx, workspace.DiscoveryConfig{
		Dir:    dir,
		APIKey: apiKey,
		Model:  addLogModel,
	})
	if err != nil {
		return err
	}

	slog.Info("Discovery completed", "run", result.RunID, "patterns", result.PatternCount)
	span.SetStatus(codes.Ok, "")
	return nil
}

func copyLogToWorkspace(ctx context.Context, dir string, args []string, setSpanAttributes func(...attribute.KeyValue)) error {
	if addLogGCPProject != "" {
		if addLogStdin {
			return errors.New("--stdin and --gcp-project are mutually exclusive")
		}
		if len(args) > 0 {
			return errors.New("logfile argument and --gcp-project are mutually exclusive")
		}
		return importGCPLogs(ctx, dir, setSpanAttributes)
	}

	if addLogStdin {
		name := fmt.Sprintf("stdin-%d.log", time.Now().UnixNano())
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return errors.Errorf("read stdin: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "logs", name), data, 0o644); err != nil {
			return errors.Errorf("write stdin log: %w", err)
		}
		slog.Info("Added stdin log", "name", name)
		return nil
	}

	if len(args) < 1 {
		return errors.New("logfile argument required (or use --stdin)")
	}
	logFile := args[0]
	setSpanAttributes(attribute.String("log.file", logFile))

	data, err := os.ReadFile(logFile)
	if err != nil {
		return errors.Errorf("read log file: %w", err)
	}
	dest := filepath.Join(dir, "logs", filepath.Base(logFile))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return errors.Errorf("copy log file: %w", err)
	}
	slog.Info("Added log file", "file", filepath.Base(logFile))
	return nil
}

// importGCPLogs fetches Cloud Logging entries into the workspace logs
// directory as a regular log file.
func importGCPLogs(ctx context.Context, dir string, setSpanAttributes func(...attribute.KeyValue)) error {
	setSpanAttributes(attribute.String("gcp.project", addLogGCPProject))

	name := fmt.Sprintf("gcp-%s-%s.log", addLogGCPProject, time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, "logs", name)
	file, err := os.Create(path)
	if err != nil {
		return errors.Errorf("create import log file: %w", err)
	}

	count, fetchErr := gcplog.NewFetcher().Fetch(ctx, gcplog.FetchConfig{
		ProjectID: addLogGCPProject,
		Filter:    addLogGCPFilter,
		Since:     time.Now().Add(-addLogGCPSince),
		Limit:     addLogGCPLimit,
		OnProgress: func(fetched int) {
			slog.Info("Fetching GCP logs", "entries", fetched)
		},
	}, file)
	closeErr := file.Close()
	if fetchErr != nil || closeErr != nil || count == 0 {
		_ = os.Remove(path)
	}
	if fetchErr != nil {
		return errors.Errorf("fetch gcp logs: %w", fetchErr)
	}
	if closeErr != nil {
		return errors.Errorf("close import log file: %w", closeErr)
	}
	if count == 0 {
		return errors.New("no log entries matched the filter and time range")
	}

	slog.Info("Imported GCP logs", "name", name, "entries", count)
	return nil
}

var analyzeWsModel string
var analyzeWsACP string
var analyzeTopic string

func workspaceAnalyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze [question]",
		Short: "Run an AI agent to analyze the workspace",
		Long: `Run an AI agent on a structured workspace directory to analyze logs.

Use --acp to choose ACP agent backend (claude/codex).`,
		Args: cobra.MaximumNArgs(1),
		RunE: runWorkspaceAnalyze,
	}
	cmd.Flags().StringVar(&analyzeTopic, "topic", "", "workspace topic (required)")
	cmd.Flags().StringVar(&analyzeWsModel, "model", "", "override ACP agent model (passed as --model to provider command)")
	cmd.Flags().StringVar(&analyzeWsACP, "acp", analyzer.ProviderClaude, "ACP agent provider: claude|codex")
	_ = cmd.MarkFlagRequired("topic")
	return cmd
}

func runWorkspaceAnalyze(cmd *cobra.Command, args []string) error {
	dir, err := topicToDir(analyzeTopic)
	if err != nil {
		return err
	}

	// Validate workspace exists
	if _, err := os.Stat(filepath.Join(dir, "logs")); os.IsNotExist(err) {
		hint := availableWorkspacesHint()
		return errors.Errorf("not a workspace: %s (no logs/ directory)%s", dir, hint)
	}

	var question string
	if len(args) > 0 {
		question = args[0]
	}

	ctx, span := otel.Tracer("lapp/cmd").Start(cmd.Context(), "cmd.WorkspaceAnalyze")
	defer span.End()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return errors.Errorf("resolve workspace dir: %w", err)
	}
	records, err := workspace.ListDiscoveryRunRecords(absDir)
	if err != nil {
		return errors.Errorf("list discovery runs: %w", err)
	}
	latestSuccess := workspace.LatestSuccessfulDiscoveryRun(records)
	if latestSuccess == nil {
		return errors.Errorf("no successful discovery run found for workspace %q; run `lapp workspace add-log --topic %s <logfile>` first", filepath.Base(absDir), filepath.Base(absDir))
	}

	tapePath := filepath.Join(absDir, ".tape.jsonl")
	config := analyzer.Config{
		Provider: analyzeWsACP,
		Model:    analyzeWsModel,
		TapePath: tapePath,
	}

	resultDir := workspace.DiscoveryRunDir(absDir, latestSuccess.ID)
	prompt := analyzer.BuildDiscoveryRunSystemPrompt(absDir, resultDir)
	result, err := analyzer.RunAgentWithPrompt(ctx, config, absDir, question, prompt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	slog.Info(result)

	span.SetStatus(codes.Ok, "")
	return nil
}
