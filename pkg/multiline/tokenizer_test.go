package multiline

import (
	"testing"
)

func TestTokenizerBasicTimestamp(t *testing.T) {
	tok := newTokenizer(60)
	tokens, _ := tok.tokenize([]byte("2024-03-28 13:45:30"))
	if len(tokens) == 0 {
		t.Fatal("expected non-empty token sequence")
	}
	str := tokensToString(tokens)
	if str == "" {
		t.Fatal("expected non-empty token string")
	}
}

func TestTokenizerSpecialTokens(t *testing.T) {
	tok := newTokenizer(100)

	tests := []struct {
		input    string
		contains Token
	}{
		{"T", tT},
		{"Z", tZone},
		{"AM", tApm},
		{"PM", tApm},
		{"Jan", tMonth},
		{"Mon", tDay},
		{"UTC", tZone},
		{"PST", tZone},
		{"CEST", tZone},
	}

	for _, tt := range tests {
		tokens, _ := tok.tokenize([]byte(tt.input))
		found := false
		for _, tok := range tokens {
			if tok == tt.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tokenize(%q) = %v, expected to contain token %d", tt.input, tokens, tt.contains)
		}
	}
}

func TestTokenizerDigitRuns(t *testing.T) {
	tok := newTokenizer(60)

	tests := []struct {
		input         string
		expectedFirst Token
	}{
		{"1", tD1},
		{"12", tD2},
		{"123", tD3},
		{"1234", tD4},
	}

	for _, tt := range tests {
		tokens, _ := tok.tokenize([]byte(tt.input))
		if len(tokens) != 1 {
			t.Errorf("tokenize(%q): expected 1 token, got %d", tt.input, len(tokens))
			continue
		}
		if tokens[0] != tt.expectedFirst {
			t.Errorf("tokenize(%q): expected token %d, got %d", tt.input, tt.expectedFirst, tokens[0])
		}
	}
}

func TestTokenizerEmpty(t *testing.T) {
	tok := newTokenizer(60)
	tokens, indices := tok.tokenize([]byte{})
	if tokens != nil || indices != nil {
		t.Errorf("expected nil for empty input, got %v, %v", tokens, indices)
	}
}

func TestIsMatch(t *testing.T) {
	a := []Token{tD4, tDash, tD2, tDash, tD2}
	b := []Token{tD4, tDash, tD2, tDash, tD2}
	if !isMatch(a, b, 1.0) {
		t.Error("identical sequences should match at threshold 1.0")
	}

	c := []Token{tD4, tDash, tD2, tDash, tD3}
	if !isMatch(a, c, 0.5) {
		t.Error("4/5 matching should pass 0.5 threshold")
	}
}
