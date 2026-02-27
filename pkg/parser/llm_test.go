package parser

import (
	"testing"
)

func TestLLMParser_AlwaysUnmatched(t *testing.T) {
	p := NewLLMParser()

	lines := []string{
		"INFO some log message",
		`{"level":"info","msg":"hello"}`,
		"Jan  5 14:32:01 myhost sshd[12345]: test",
	}

	for _, line := range lines {
		result := p.Parse(line)
		if result.Matched {
			t.Errorf("LLMParser stub should never match, but matched: %q", line)
		}
	}
}

func TestLLMParser_EmptyTemplates(t *testing.T) {
	p := NewLLMParser()

	templates := p.Templates()
	if len(templates) != 0 {
		t.Errorf("expected 0 templates from LLM stub, got %d", len(templates))
	}
}
