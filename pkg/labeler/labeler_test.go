package labeler

import (
	"strings"
	"testing"

	llmconfig "github.com/strrl/lapp/pkg/config"
)

func TestBuildPrompt(t *testing.T) {
	patterns := []PatternInput{
		{
			PatternUUID: "D1",
			Pattern:     "Starting <*> on port <*>",
			Samples:     []string{"Starting myapp on port 8080", "Starting worker on port 3000"},
		},
		{
			PatternUUID: "D2",
			Pattern:     "Connection timeout after <*> ms",
			Samples:     []string{"Connection timeout after 5000 ms"},
		},
	}

	prompt := buildPrompt(patterns)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if len(prompt) < 50 {
		t.Errorf("prompt too short: %d chars", len(prompt))
	}
	for _, want := range []string{"D1", "D2", "Starting <*> on port <*>", "Connection timeout", "Starting myapp on port 8080"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing expected content %q", want)
		}
	}
}

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:  "plain JSON array",
			input: `[{"pattern_id":"D1","semantic_id":"server-startup","description":"Server starting on a port"}]`,
			want:  1,
		},
		{
			name: "with markdown code fences (rejected since JSON mode guarantees clean output)",
			input: "```json\n" +
				`[{"pattern_id":"D1","semantic_id":"server-startup","description":"Server starting"}]` +
				"\n```",
			wantErr: true,
		},
		{
			name: "multiple labels",
			input: `[
				{"pattern_id":"D1","semantic_id":"server-startup","description":"Server starting"},
				{"pattern_id":"D2","semantic_id":"conn-timeout","description":"Connection timeout"}
			]`,
			want: 2,
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "code fence without closing fence (rejected since JSON mode guarantees clean output)",
			input:   "```json\n" + `[{"pattern_id":"D1","semantic_id":"test","description":"test"}]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels, err := parseResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(labels) != tt.want {
				t.Errorf("got %d labels, want %d", len(labels), tt.want)
			}
		})
	}
}

func TestResolveModel(t *testing.T) {
	// Explicit model takes priority
	got := llmconfig.ResolveModel("my-model")
	if got != "my-model" {
		t.Errorf("got %q, want %q", got, "my-model")
	}

	// MODEL_NAME env var takes priority over default
	t.Setenv("MODEL_NAME", "env-model")
	got = llmconfig.ResolveModel("")
	if got != "env-model" {
		t.Errorf("got %q, want %q", got, "env-model")
	}

	// Explicit model still wins over env var
	got = llmconfig.ResolveModel("explicit")
	if got != "explicit" {
		t.Errorf("got %q, want %q", got, "explicit")
	}

	// Falls back to default when env is unset
	t.Setenv("MODEL_NAME", "")
	got = llmconfig.ResolveModel("")
	if got != llmconfig.DefaultModel {
		t.Errorf("got %q, want %q", got, llmconfig.DefaultModel)
	}
}
