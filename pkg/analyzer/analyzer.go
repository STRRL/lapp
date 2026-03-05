package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/adk"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/go-errors/errors"
	llmconfig "github.com/strrl/lapp/pkg/config"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
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
	APIKey string
	Model  string
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

	config.Model = llmconfig.ResolveModel(config.Model)
	span.SetAttributes(attribute.String("model", config.Model))

	absDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", errors.Errorf("resolve workspace dir: %w", err)
	}

	slog.Info("Analyzing with model", "model", config.Model)

	// Preflight check: verify API key works
	if err := preflightCheck(ctx, config); err != nil {
		return "", err
	}

	// Create OpenRouter chat model with fixup transport to patch eino tool message bug
	// Stack: otelhttp (tracing) -> fixupRoundTripper (eino bug workaround) -> http.DefaultTransport
	chatModel, err := openrouter.NewChatModel(ctx, &openrouter.Config{
		APIKey: config.APIKey,
		Model:  config.Model,
		HTTPClient: &http.Client{
			Transport: otelhttp.NewTransport(&fixupRoundTripper{base: http.DefaultTransport}),
		},
	})
	if err != nil {
		return "", errors.Errorf("create chat model: %w", err)
	}

	// Create local filesystem backend from eino-ext
	backend, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		return "", errors.Errorf("create local backend: %w", err)
	}

	// Create filesystem middleware
	fsMiddleware, err := fsmw.NewMiddleware(ctx, &fsmw.Config{
		Backend: backend,
	})
	if err != nil {
		return "", errors.Errorf("create filesystem middleware: %w", err)
	}

	// Use the provided system prompt, replacing workDir placeholder if needed
	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(absDir)
	}

	// Create agent
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "log-analyzer",
		Description:   "Analyzes log files to find root causes",
		Instruction:   systemPrompt,
		Model:         chatModel,
		Middlewares:   []adk.AgentMiddleware{fsMiddleware},
		MaxIterations: 15,
	})
	if err != nil {
		return "", errors.Errorf("create agent: %w", err)
	}

	// Build user message
	userMessage := "Analyze the log files in the workspace."
	if question != "" {
		userMessage = "Analyze the log files in the workspace. The user's question: " + question
	}

	// Run agent
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent,
	})

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

// fixupRoundTripper patches outgoing API requests to work around eino bugs.
type fixupRoundTripper struct {
	base http.RoundTripper
}

func (rt *fixupRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Patch tool messages missing "content" field before sending to OpenRouter.
	// eino omits "content" when a tool returns empty results (e.g. grep with no matches),
	// which causes the Anthropic API to return 500.
	if req.Body != nil && req.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, errors.Errorf("read request body: %w", err)
		}
		bodyBytes = fixToolMessages(bodyBytes)
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}
	return rt.base.RoundTrip(req)
}

func fixToolMessages(body []byte) []byte {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	messagesRaw, ok := payload["messages"]
	if !ok {
		return body
	}
	var messages []map[string]any
	if err := json.Unmarshal(messagesRaw, &messages); err != nil {
		return body
	}

	changed := false
	for _, msg := range messages {
		if msg["role"] == "tool" {
			if _, hasContent := msg["content"]; !hasContent {
				msg["content"] = ""
				changed = true
			}
		}
	}
	if !changed {
		return body
	}

	fixedMessages, err := json.Marshal(messages)
	if err != nil {
		return body
	}
	payload["messages"] = fixedMessages
	result, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return result
}

// preflightCheck does a quick API call to verify the key works.
func preflightCheck(ctx context.Context, config Config) error {
	_, span := otel.Tracer("lapp/analyzer").Start(ctx, "analyzer.PreflightCheck")
	defer span.End()

	apiURL := "https://openrouter.ai/api/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return errors.Errorf("preflight: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Errorf("preflight: cannot reach OpenRouter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return errors.Errorf("API error (HTTP %d) from OpenRouter: %s", resp.StatusCode, string(body))
	}
	return nil
}
