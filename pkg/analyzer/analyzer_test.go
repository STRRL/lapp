package analyzer

import (
	"strings"
	"testing"
)

func TestBuildDiscoveryRunSystemPromptUsesRunScopedResults(t *testing.T) {
	prompt := BuildDiscoveryRunSystemPrompt("/tmp/workspaces/app", "/tmp/workspaces/app/discovery-runs/run-1")

	for _, want := range []string{
		"/tmp/workspaces/app/logs/",
		"/tmp/workspaces/app/discovery-runs/run-1/patterns/",
		"/tmp/workspaces/app/discovery-runs/run-1/notes/summary.md",
		"/tmp/workspaces/app/discovery-runs/run-1/notes/errors.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, prompt)
		}
	}
}
