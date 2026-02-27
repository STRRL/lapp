package labeler

import (
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	patterns := []PatternInput{
		{
			PatternID: "D1",
			Pattern:   "Starting <*> on port <*>",
			Samples:   []string{"Starting myapp on port 8080", "Starting worker on port 3000"},
		},
		{
			PatternID: "D2",
			Pattern:   "Connection timeout after <*> ms",
			Samples:   []string{"Connection timeout after 5000 ms"},
		},
	}

	prompt := buildPrompt(patterns)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if len(prompt) < 50 {
		t.Errorf("prompt too short: %d chars", len(prompt))
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
			input: `[{"template_id":"D1","semantic_id":"server-startup","description":"Server starting on a port"}]`,
			want:  1,
		},
		{
			name: "with markdown code fences",
			input: "```json\n" +
				`[{"template_id":"D1","semantic_id":"server-startup","description":"Server starting"}]` +
				"\n```",
			want: 1,
		},
		{
			name: "multiple labels",
			input: `[
				{"template_id":"D1","semantic_id":"server-startup","description":"Server starting"},
				{"template_id":"D2","semantic_id":"conn-timeout","description":"Connection timeout"}
			]`,
			want: 2,
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
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
	got := resolveModel("my-model")
	if got != "my-model" {
		t.Errorf("got %q, want %q", got, "my-model")
	}

	// Empty falls back to default
	got = resolveModel("")
	if got == "" {
		t.Error("expected non-empty default model")
	}
}
