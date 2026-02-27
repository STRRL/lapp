package labeler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultModel = "google/gemini-3-flash-preview"

// Config holds configuration for the labeler.
type Config struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// PatternInput represents a pattern to be labeled.
type PatternInput struct {
	PatternID string
	Pattern   string
	Samples   []string
}

// SemanticLabel is the LLM-generated label for a pattern.
type SemanticLabel struct {
	PatternID   string `json:"pattern_id"`
	SemanticID  string `json:"semantic_id"`
	Description string `json:"description"`
}

func resolveModel(model string) string {
	if model != "" {
		return model
	}
	if env := os.Getenv("MODEL_NAME"); env != "" {
		return env
	}
	return defaultModel
}

// Label sends all patterns to the LLM in a single call and returns semantic labels.
func Label(ctx context.Context, config Config, patterns []PatternInput) ([]SemanticLabel, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	config.Model = resolveModel(config.Model)

	prompt := buildPrompt(patterns)
	resp, err := callLLM(ctx, config, prompt)
	if err != nil {
		return nil, fmt.Errorf("call LLM: %w", err)
	}

	labels, err := parseResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}

	return labels, nil
}

func buildPrompt(patterns []PatternInput) string {
	var b strings.Builder
	b.WriteString(`You are a log analysis expert. Given the following log patterns and sample lines, generate a short semantic_id (kebab-case, max 30 chars) and a one-line description for each.

Output ONLY a JSON array with no markdown formatting, like:
[{"pattern_id": "D1", "semantic_id": "server-startup", "description": "Server process starting on a specific port"}]

Patterns:
`)

	for _, p := range patterns {
		fmt.Fprintf(&b, "\nPattern %s: %q\n", p.PatternID, p.Pattern)
		if len(p.Samples) > 0 {
			b.WriteString("Samples:\n")
			for _, s := range p.Samples {
				fmt.Fprintf(&b, "  - %s\n", s)
			}
		}
	}

	return b.String()
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func callLLM(ctx context.Context, config Config, prompt string) (string, error) {
	reqBody := chatRequest{
		Model: config.Model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

func parseResponse(content string) ([]SemanticLabel, error) {
	// Strip markdown code fences if present
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			// Strip the opening fence line
			lines = lines[1:]
			// Strip the closing fence line if present
			if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
		}
		content = strings.Join(lines, "\n")
	}

	var labels []SemanticLabel
	if err := json.Unmarshal([]byte(content), &labels); err != nil {
		return nil, fmt.Errorf("JSON decode (content=%q): %w", content[:min(len(content), 200)], err)
	}
	return labels, nil
}
