package parser

import (
	"testing"

	"github.com/google/uuid"
)

func TestChainParser_FirstMatchWins(t *testing.T) {
	jp := NewJSONParser()
	dp, err := NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}
	chain := NewChainParser(jp, dp)

	// JSON line should be caught by JSONParser first
	jsonLine := `{"level":"info","message":"hello world"}`
	result := chain.Parse(jsonLine)
	if !result.Matched {
		t.Fatal("expected chain to match JSON line")
	}
	if result.Pattern != "hello world" {
		t.Errorf("expected template 'hello world', got %q", result.Pattern)
	}

	// Non-JSON line should fall through to Drain
	plainLine := "081109 203615 148 INFO dfs.DataNode: some log message"
	result = chain.Parse(plainLine)
	if !result.Matched {
		t.Fatal("expected chain to match plain line via Drain")
	}
	if result.PatternID == "" {
		t.Error("expected non-empty PatternID from Drain")
	}
}

func TestChainParser_NoMatch(t *testing.T) {
	lp := NewLLMParser()
	chain := NewChainParser(lp)

	result := chain.Parse("any log line")
	if result.Matched {
		t.Error("expected chain with only LLM stub to not match")
	}
}

func TestChainParser_Templates(t *testing.T) {
	jp := NewJSONParser()
	dp, err := NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}
	chain := NewChainParser(jp, dp)

	// Feed some data so templates are generated
	chain.Parse(`{"level":"info","message":"hello"}`)
	chain.Parse("INFO some log line here")
	chain.Parse("INFO another log line here")

	templates := chain.Templates()
	if len(templates) == 0 {
		t.Error("expected templates from chain after parsing")
	}

	// Should have templates from both JSON and Drain
	hasJSON := false
	hasDrain := false
	for _, tmpl := range templates {
		if len(tmpl.ID) > 0 && tmpl.ID[0] == 'J' {
			hasJSON = true
		}
		if _, err := uuid.Parse(tmpl.ID); err == nil {
			hasDrain = true
		}
	}
	if !hasJSON {
		t.Error("expected at least one JSON template in chain")
	}
	if !hasDrain {
		t.Error("expected at least one Drain (UUID) template in chain")
	}
}

func TestChainParser_Order(t *testing.T) {
	// Drain first, JSON second - Drain should catch JSON lines too
	dp, err := NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}
	jp := NewJSONParser()
	chain := NewChainParser(dp, jp)

	jsonLine := `{"level":"info","message":"test"}`
	result := chain.Parse(jsonLine)
	if !result.Matched {
		t.Fatal("expected chain to match")
	}
	// Drain catches everything, so it should match first (UUID format)
	if _, err := uuid.Parse(result.PatternID); err != nil {
		t.Errorf("expected Drain to match first with UUID pattern ID, got %q", result.PatternID)
	}
}
