package analyzer

import (
	"reflect"
	"testing"
)

func TestBuildACPCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		provider     string
		model        string
		wantProvider string
		wantCommand  []string
	}{
		{
			name:         "default provider",
			provider:     "",
			wantProvider: ProviderClaude,
			wantCommand:  []string{"npx", "-y", "@zed-industries/claude-agent-acp@latest"},
		},
		{
			name:         "codex with model",
			provider:     ProviderCodex,
			model:        "gpt-5-codex",
			wantProvider: ProviderCodex,
			wantCommand:  []string{"codex", "--acp", "--model", "gpt-5-codex"},
		},
		{
			name:         "gemini normalized",
			provider:     "GeMiNi",
			wantProvider: ProviderGemini,
			wantCommand:  []string{"gemini", "--experimental-acp"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, command, err := BuildACPCommand(tc.provider, tc.model)
			if err != nil {
				t.Fatalf("BuildACPCommand() error = %v", err)
			}
			if provider != tc.wantProvider {
				t.Fatalf("provider = %q, want %q", provider, tc.wantProvider)
			}
			if !reflect.DeepEqual(command, tc.wantCommand) {
				t.Fatalf("command = %#v, want %#v", command, tc.wantCommand)
			}
		})
	}
}

func TestBuildACPCommand_InvalidProvider(t *testing.T) {
	t.Parallel()

	_, _, err := BuildACPCommand("openrouter", "")
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
}
