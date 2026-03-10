package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/go-errors/errors"
	einoacp "github.com/strrl/eino-acp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

func buildSystemPrompt(workDir string) string {
	return fmt.Sprintf(`You are a log analysis expert helping developers troubleshoot issues.

IMPORTANT: All file operations (read_file, grep, ls, glob, execute) MUST use paths under %s.
Do NOT access files outside this workspace directory.

Your workspace contains pre-processed log data at %s:
- %s/raw.log — the original log file
- %s/summary.txt — log templates discovered by automated parsing, with occurrence counts and samples
- %s/errors.txt — error and warning patterns extracted from logs

Start by reading %s/summary.txt and %s/errors.txt to understand the log patterns.
Then use grep and read_file on %s/raw.log to investigate specific patterns in detail.
You can also use the execute tool to run shell commands (e.g., awk, sort, wc) for deeper analysis.

Provide:
1. Key findings from the logs
2. Anomalies or error patterns detected
3. Root cause analysis (if a problem description is provided)
4. Suggested next steps for debugging

Be concise and actionable. Focus on what matters.`,
		workDir, workDir, workDir, workDir, workDir, workDir, workDir, workDir)
}

// Config holds configuration for the analyzer.
type Config struct {
	Provider string
	Model    string
}

// BuildWorkspaceSystemPrompt builds a system prompt for the structured workspace layout.
func BuildWorkspaceSystemPrompt(workDir string) string {
	return fmt.Sprintf(`You are a log analysis expert helping developers troubleshoot issues.

IMPORTANT: All file operations (read_file, grep, ls, glob, execute) MUST use paths under %s.
Do NOT access files outside this workspace directory.

Your workspace at %s contains structured log data:
- %s/logs/ — original log files
- %s/patterns/ — discovered log patterns, one directory per pattern
  - Each pattern directory contains pattern.md (metadata) and samples.log (sample lines)
  - %s/patterns/unmatched/samples.log — lines that did not match any pattern
- %s/notes/summary.md — overview of all patterns sorted by frequency
- %s/notes/errors.md — error and warning patterns

Start by reading %s/notes/summary.md and %s/notes/errors.md to understand the log patterns.
Then drill into specific patterns under %s/patterns/ for details.
Use grep on %s/logs/ to search for specific terms across all log files.
You can also use the execute tool to run shell commands (e.g., awk, sort, wc) for deeper analysis.

Provide:
1. Key findings from the logs
2. Anomalies or error patterns detected
3. Root cause analysis (if a problem description is provided)
4. Suggested next steps for debugging

Be concise and actionable. Focus on what matters.`,
		workDir, workDir, workDir, workDir, workDir, workDir, workDir, workDir, workDir, workDir, workDir)
}

// RunAgentWithPrompt runs the AI agent on an existing workspace directory with a custom system prompt.
//
//nolint:gocyclo // sequential setup of model, backend, middleware, agent, and runner
func RunAgentWithPrompt(ctx context.Context, config Config, workDir, question, systemPrompt string) (string, error) {
	ctx, span := otel.Tracer("lapp/analyzer").Start(ctx, "analyzer.RunAgentWithPrompt")
	defer span.End()

	span.SetAttributes(
		attribute.String("workspace.dir", workDir),
	)

	absDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", errors.Errorf("resolve workspace dir: %w", err)
	}

	provider, command, err := BuildACPCommand(config.Provider, config.Model)
	if err != nil {
		return "", err
	}

	span.SetAttributes(
		attribute.String("provider", provider),
		attribute.String("model", config.Model),
	)

	slog.Info("Analyzing with ACP provider", "provider", provider, "model", config.Model)

	chatModel, err := einoacp.NewChatModel(ctx, &einoacp.Config{
		Command:     command,
		Cwd:         absDir,
		AutoApprove: true,
	})
	if err != nil {
		return "", errors.Errorf("create chat model: %w", err)
	}

	backend, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		return "", errors.Errorf("create local backend: %w", err)
	}
	backendAdapter := newLocalBackendAdapter(backend)

	fsHandler, err := fsmw.New(ctx, &fsmw.MiddlewareConfig{
		Backend:        backendAdapter,
		StreamingShell: backendAdapter,
	})
	if err != nil {
		return "", errors.Errorf("create filesystem middleware: %w", err)
	}

	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(absDir)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "log-analyzer",
		Description:   "Analyzes log files to find root causes",
		Instruction:   systemPrompt,
		Model:         newACPToolCallingModel(chatModel),
		Handlers:      []adk.ChatModelAgentMiddleware{fsHandler},
		MaxIterations: 15,
	})
	if err != nil {
		return "", errors.Errorf("create agent: %w", err)
	}

	userMessage := "Analyze the log files in the workspace."
	if question != "" {
		userMessage = "Analyze the log files in the workspace. The user's question: " + question
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Query(ctx, userMessage)

	var result strings.Builder
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return "", errors.Errorf("agent error: %w", event.Err)
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil {
			continue
		}
		if msg != nil && msg.Role == "assistant" && msg.Content != "" {
			result.WriteString(msg.Content)
		}
	}

	return result.String(), nil
}

// RunAgent runs the AI agent on an existing workspace directory using the default system prompt.
func RunAgent(ctx context.Context, config Config, workDir, question string) (string, error) {
	return RunAgentWithPrompt(ctx, config, workDir, question, "")
}
