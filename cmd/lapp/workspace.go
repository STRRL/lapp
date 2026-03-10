package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/analyzer"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/workspace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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

	for _, sub := range []string{"logs", "patterns", "notes"} {
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

func workspaceAddLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-log [logfile]",
		Short: "Add a log file to the workspace and rebuild patterns",
		Long: `Copy a log file into the workspace's logs/ directory, then run the full
pipeline (Drain clustering + semantic labeling) to regenerate patterns/ and notes/.

Requires OPENROUTER_API_KEY environment variable.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runWorkspaceAddLog,
	}
	cmd.Flags().StringVar(&addLogTopic, "topic", "", "workspace topic (required)")
	cmd.Flags().StringVar(&addLogModel, "model", "", "override LLM model")
	cmd.Flags().BoolVar(&addLogStdin, "stdin", false, "read log from stdin")
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

	if err := copyLogToWorkspace(dir, args, span); err != nil {
		return err
	}

	allTagged, allContent, fileCount, err := mergeAllLogs(ctx, dir)
	if err != nil {
		return err
	}

	slog.Info("Processing logs", "files", fileCount, "lines", len(allTagged))

	filtered, err := runDrain(ctx, allContent)
	if err != nil {
		return err
	}

	labels, err := labelPatterns(ctx, filtered, allContent, apiKey)
	if err != nil {
		return err
	}

	if err := resetWorkspaceDirs(dir); err != nil {
		return err
	}

	builder := workspace.NewBuilder(dir, allTagged, filtered, labels)
	if err := builder.BuildAll(); err != nil {
		return errors.Errorf("build workspace: %w", err)
	}

	slog.Info("Workspace rebuilt", "patterns", len(filtered))
	span.SetStatus(codes.Ok, "")
	return nil
}

func copyLogToWorkspace(dir string, args []string, span trace.Span) error {
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
	span.SetAttributes(attribute.String("log.file", logFile))

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

func mergeAllLogs(ctx context.Context, dir string) (tagged []workspace.TaggedLine, content []string, fileCount int, err error) {
	allLogs, err := workspace.ReadAllLogs(dir)
	if err != nil {
		return nil, nil, 0, errors.Errorf("read all logs: %w", err)
	}

	// Sort filenames for deterministic output across rebuilds
	fileNames := make([]string, 0, len(allLogs))
	for name := range allLogs {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	var allTagged []workspace.TaggedLine
	var allContent []string
	for _, fileName := range fileNames {
		lines := allLogs[fileName]
		detector, err := multiline.NewDetector(multiline.DetectorConfig{})
		if err != nil {
			return nil, nil, 0, errors.Errorf("multiline detector: %w", err)
		}
		merged := multiline.MergeSlice(ctx, lines, detector)
		for _, m := range merged {
			allTagged = append(allTagged, workspace.TaggedLine{
				Content:  m.Content,
				FileName: fileName,
				LineNum:  m.StartLine,
			})
			allContent = append(allContent, m.Content)
		}
	}
	return allTagged, allContent, len(allLogs), nil
}

func runDrain(ctx context.Context, content []string) ([]pattern.DrainCluster, error) {
	drainParser, err := pattern.NewDrainParser()
	if err != nil {
		return nil, errors.Errorf("drain parser: %w", err)
	}
	if err := drainParser.Feed(ctx, content); err != nil {
		return nil, errors.Errorf("drain feed: %w", err)
	}
	templates, err := drainParser.Templates(ctx)
	if err != nil {
		return nil, errors.Errorf("drain templates: %w", err)
	}

	var filtered []pattern.DrainCluster
	for _, t := range templates {
		if t.Count > 1 {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

func labelPatterns(ctx context.Context, filtered []pattern.DrainCluster, content []string, apiKey string) ([]semantic.SemanticLabel, error) {
	if len(filtered) == 0 {
		return nil, nil
	}
	inputs := buildLabelInputs(ctx, filtered, content)
	slog.Info("Labeling patterns", "count", len(inputs))
	labels, err := semantic.Label(ctx, semantic.Config{
		APIKey: apiKey,
		Model:  addLogModel,
	}, inputs)
	if err != nil {
		return nil, errors.Errorf("label: %w", err)
	}
	return labels, nil
}

func resetWorkspaceDirs(dir string) error {
	for _, sub := range []string{"patterns", "notes"} {
		subDir := filepath.Join(dir, sub)
		if err := os.RemoveAll(subDir); err != nil {
			return errors.Errorf("remove %s: %w", sub, err)
		}
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return errors.Errorf("create %s: %w", sub, err)
		}
	}
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
	if _, err := os.Stat(filepath.Join(dir, "patterns")); os.IsNotExist(err) {
		hint := availableWorkspacesHint()
		return errors.Errorf("not a workspace: %s (no patterns/ directory)%s", dir, hint)
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

	config := analyzer.Config{
		Provider: analyzeWsACP,
		Model:    analyzeWsModel,
	}

	prompt := analyzer.BuildWorkspaceSystemPrompt(absDir)
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

func buildLabelInputs(ctx context.Context, templates []pattern.DrainCluster, lines []string) []semantic.PatternInput {
	_, span := otel.Tracer("lapp/pipeline").Start(ctx, "pipeline.BuildLabelInputs")
	defer span.End()

	span.SetAttributes(attribute.Int("template.count", len(templates)))

	var inputs []semantic.PatternInput
	for _, t := range templates {
		var samples []string
		for _, line := range lines {
			if _, ok := pattern.MatchTemplate(line, []pattern.DrainCluster{t}); ok {
				samples = append(samples, line)
				if len(samples) >= 3 {
					break
				}
			}
		}
		inputs = append(inputs, semantic.PatternInput{
			PatternUUIDString: t.ID.String(),
			Pattern:           t.Pattern,
			Samples:           samples,
		})
	}
	return inputs
}
