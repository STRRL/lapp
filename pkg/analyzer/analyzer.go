package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/adk"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/strrl/lapp/pkg/parser"
)

const defaultModel = "anthropic/claude-sonnet-4.6"

func buildSystemPrompt(workDir string) string {
	return fmt.Sprintf(`You are a log analysis expert helping developers troubleshoot issues.

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
		workDir, workDir, workDir, workDir, workDir, workDir, workDir)
}

// Config holds configuration for the analyzer.
type Config struct {
	APIKey string
	Model  string
}

// Analyze runs the agentic log analysis pipeline.
func Analyze(ctx context.Context, config Config, lines []string, question string) (string, error) {
	if config.Model == "" {
		config.Model = defaultModel
	}

	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "lapp-analyze-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Resolve to absolute path for the local backend
	absDir, err := filepath.Abs(tmpDir)
	if err != nil {
		return "", fmt.Errorf("resolve temp dir: %w", err)
	}

	// Build parser chain and workspace
	grokParser, err := parser.NewGrokParser()
	if err != nil {
		return "", fmt.Errorf("grok parser: %w", err)
	}
	chain := parser.NewChainParser(
		parser.NewJSONParser(),
		grokParser,
		parser.NewDrainParser(),
	)

	fmt.Fprintf(os.Stderr, "Parsing %d lines...\n", len(lines))
	if err := BuildWorkspace(absDir, lines, chain); err != nil {
		return "", fmt.Errorf("build workspace: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Analyzing with model %s...\n", config.Model)

	// Preflight check: verify API key works
	if err := preflightCheck(config); err != nil {
		return "", err
	}

	// Create OpenRouter chat model with fixup transport to patch eino tool message bug
	chatModel, err := openrouter.NewChatModel(ctx, &openrouter.Config{
		APIKey:     config.APIKey,
		Model:      config.Model,
		HTTPClient: &http.Client{Transport: &fixupRoundTripper{base: http.DefaultTransport}},
	})
	if err != nil {
		return "", fmt.Errorf("create chat model: %w", err)
	}

	// Create local filesystem backend from eino-ext
	backend, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		return "", fmt.Errorf("create local backend: %w", err)
	}

	// Create filesystem middleware
	fsMiddleware, err := fsmw.NewMiddleware(ctx, &fsmw.Config{
		Backend: backend,
	})
	if err != nil {
		return "", fmt.Errorf("create filesystem middleware: %w", err)
	}

	// Create agent
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "log-analyzer",
		Description:   "Analyzes log files to find root causes",
		Instruction:   buildSystemPrompt(absDir),
		Model:         chatModel,
		Middlewares:   []adk.AgentMiddleware{fsMiddleware},
		MaxIterations: 15,
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	// Build user message
	userMessage := "Analyze the log files in the workspace."
	if question != "" {
		userMessage = fmt.Sprintf("Analyze the log files in the workspace. The user's question: %s", question)
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
			return "", fmt.Errorf("agent error: %w", event.Err)
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

// fixupRoundTripper patches outgoing API requests to work around eino bugs.
type fixupRoundTripper struct {
	base http.RoundTripper
}

func (rt *fixupRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Patch tool messages missing "content" field before sending to OpenRouter.
	// eino omits "content" when a tool returns empty results (e.g. grep with no matches),
	// which causes the Anthropic API to return 500.
	if req.Body != nil && req.Method == "POST" {
		bodyBytes, _ := io.ReadAll(req.Body)
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
func preflightCheck(config Config) error {
	apiURL := "https://openrouter.ai/api/v1/models"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("preflight: cannot reach OpenRouter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("API error (HTTP %d) from OpenRouter: %s", resp.StatusCode, string(body))
	}
	return nil
}
