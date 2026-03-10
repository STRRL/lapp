package analyzer

import (
	"strings"

	"github.com/go-errors/errors"
	einoacp "github.com/strrl/eino-acp"
)

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

// BuildACPCommand resolves provider and builds the ACP launcher command.
func BuildACPCommand(provider, model string) (resolvedProvider string, command []string, err error) {
	resolved, err := resolveProvider(provider)
	if err != nil {
		return "", nil, err
	}

	switch resolved {
	case ProviderClaude:
		command = einoacp.ClaudeCommand()
	case ProviderCodex:
		command = einoacp.CodexCommand()
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
	case ProviderClaude, ProviderCodex:
		return normalized, nil
	default:
		return "", errors.Errorf("invalid provider %q (supported: claude, codex)", provider)
	}
}
