package parser

import (
	"testing"
)

func TestJSONParser_ValidJSON(t *testing.T) {
	p := NewJSONParser()

	line := `{"level":"info","message":"server started","port":8080}`
	result := p.Parse(line)
	if !result.Matched {
		t.Fatal("expected JSON line to match")
	}
	if result.PatternID == "" {
		t.Error("expected non-empty PatternID")
	}
	// Should extract "message" field as template
	if result.Pattern != "server started" {
		t.Errorf("expected pattern 'server started', got %q", result.Pattern)
	}
	if result.Params == nil {
		t.Fatal("expected non-nil Params")
	}
	if result.Params["level"] != `"info"` {
		t.Errorf("expected level param '\"info\"', got %q", result.Params["level"])
	}
}

func TestJSONParser_MsgField(t *testing.T) {
	p := NewJSONParser()

	line := `{"msg":"request handled","status":200}`
	result := p.Parse(line)
	if !result.Matched {
		t.Fatal("expected JSON line to match")
	}
	if result.Pattern != "request handled" {
		t.Errorf("expected pattern 'request handled', got %q", result.Pattern)
	}
}

func TestJSONParser_NoMessageField(t *testing.T) {
	p := NewJSONParser()

	line := `{"host":"web01","cpu":0.85}`
	result := p.Parse(line)
	if !result.Matched {
		t.Fatal("expected JSON line to match")
	}
	// Without message/msg, template should be the key list
	if result.Pattern != "cpu, host" {
		t.Errorf("expected pattern 'cpu, host', got %q", result.Pattern)
	}
}

func TestJSONParser_NonJSON(t *testing.T) {
	p := NewJSONParser()

	lines := []string{
		"plain text log line",
		"2024-01-01 INFO something happened",
		"",
		"[not json]",
	}
	for _, line := range lines {
		result := p.Parse(line)
		if result.Matched {
			t.Errorf("expected non-JSON line to not match: %q", line)
		}
	}
}

func TestJSONParser_Templates(t *testing.T) {
	p := NewJSONParser()

	p.Parse(`{"level":"info","message":"hello"}`)
	p.Parse(`{"level":"warn","message":"world"}`)
	p.Parse(`{"host":"web01","cpu":0.85}`)

	templates := p.Templates()
	if len(templates) != 2 {
		t.Errorf("expected 2 distinct templates, got %d", len(templates))
	}
}
