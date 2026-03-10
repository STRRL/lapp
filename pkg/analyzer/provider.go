package analyzer

import (
	"strings"

	"github.com/go-errors/errors"
)

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
	ProviderGemini = "gemini"
)

// BuildACPCommand resolves provider and builds the ACP launcher command.
func BuildACPCommand(provider, model string) (resolvedProvider string, command []string, err error) {
	resolved, err := resolveProvider(provider)
	if err != nil {
		return "", nil, err
	}

	switch resolved {
	case ProviderClaude:
		command = []string{"npx", "-y", "@zed-industries/claude-agent-acp@latest"}
	case ProviderCodex:
		command = []string{"codex", "--acp"}
	case ProviderGemini:
		command = []string{"gemini", "--experimental-acp"}
	default:
		return "", nil, errors.Errorf("unsupported provider %q", resolved)
	}

	if model != "" {
		command = append(command, "--model", model)
	}

	return resolved, command, nil
}

func resolveProvider(provider string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return ProviderClaude, nil
	}

	switch normalized {
	case ProviderClaude, ProviderCodex, ProviderGemini:
		return normalized, nil
	default:
		return "", errors.Errorf("invalid provider %q (supported: claude, codex, gemini)", provider)
	}
}
