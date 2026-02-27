package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/schema"
	"github.com/go-errors/errors"
	llmconfig "github.com/strrl/lapp/pkg/config"
)

// Config holds configuration for the labeler.
type Config struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// PatternInput represents a log pattern to be labeled.
//
// Fields come from the Drain log parsing algorithm:
//   - PatternUUIDString: a UUID string assigned to each Drain cluster (group of similar log lines)
//   - Pattern: the Drain template string where variable tokens are replaced with <*>
//     Example: "Starting <*> on port <*>"
//   - Samples: representative raw log lines from this cluster, used as LLM context
type PatternInput struct {
	PatternUUIDString string
	Pattern           string
	Samples           []string
}

// SemanticLabel is the LLM-generated label for a pattern.
type SemanticLabel struct {
	PatternUUIDString string `json:"pattern_id"`
	SemanticID        string `json:"semantic_id"`
	Description       string `json:"description"`
}

// Label sends all patterns to the LLM in a single call and returns semantic labels.
func Label(ctx context.Context, config Config, patterns []PatternInput) ([]SemanticLabel, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	config.Model = llmconfig.ResolveModel(config.Model)

	prompt := buildPrompt(patterns)
	resp, err := callLLM(ctx, config, prompt)
	if err != nil {
		return nil, errors.Errorf("call LLM: %w", err)
	}

	labels, err := parseResponse(resp)
	if err != nil {
		return nil, errors.Errorf("parse LLM response: %w", err)
	}

	return labels, nil
}

func buildPrompt(patterns []PatternInput) string {
	var b strings.Builder
	b.WriteString(`You are a log analysis expert. Given the following log patterns and sample lines, generate a short semantic_id (kebab-case, max 30 chars) and a one-line description for each.

Output ONLY a JSON array with no markdown formatting. Use the exact pattern_id values provided below, like:
[{"pattern_id": "<actual-pattern-id>", "semantic_id": "server-startup", "description": "Server process starting on a specific port"}]

Patterns:
`)

	for _, p := range patterns {
		fmt.Fprintf(&b, "\nPattern %s: %q\n", p.PatternUUIDString, p.Pattern)
		if len(p.Samples) > 0 {
			b.WriteString("Samples:\n")
			for _, s := range p.Samples {
				fmt.Fprintf(&b, "  - %s\n", s)
			}
		}
	}

	return b.String()
}

func callLLM(ctx context.Context, config Config, prompt string) (string, error) {
	chatModel, err := openrouter.NewChatModel(ctx, &openrouter.Config{
		APIKey:     config.APIKey,
		Model:      config.Model,
		HTTPClient: config.HTTPClient,
		ResponseFormat: &openrouter.ChatCompletionResponseFormat{
			Type: openrouter.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return "", errors.Errorf("create chat model: %w", err)
	}

	resp, err := chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", errors.Errorf("generate: %w", err)
	}
	return resp.Content, nil
}

func parseResponse(content string) ([]SemanticLabel, error) {
	content = strings.TrimSpace(content)

	var labels []SemanticLabel
	if err := json.Unmarshal([]byte(content), &labels); err != nil {
		return nil, errors.Errorf("JSON decode (content=%q): %w", content[:min(len(content), 200)], err)
	}
	return labels, nil
}
