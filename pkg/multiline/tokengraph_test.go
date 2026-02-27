package multiline

import "testing"

func TestTokenGraphMatchProbability(t *testing.T) {
	tok := newTokenizer(100)

	patterns := [][]Token{}
	for _, format := range knownTimestampFormats {
		tokens, _ := tok.tokenize([]byte(format))
		patterns = append(patterns, tokens)
	}
	graph := newTokenGraph(minimumTokenLength, patterns)

	tests := []struct {
		input     string
		expectHit bool
	}{
		{"2024-03-28 13:45:30", true},
		{"2024-03-28T13:45:30.123456Z", true},
		{"Mar 16 08:12:04", true},
		{"28/Mar/2024:13:45:30", true},
		{"hello world", false},
		{"	at com.example.Foo.bar(Foo.java:42)", false},
		{"Caused by: java.lang.NullPointerException", false},
	}

	for _, tt := range tests {
		tokens, _ := tok.tokenize([]byte(tt.input))
		result := graph.matchProbability(tokens)
		if tt.expectHit && result.probability <= 0 {
			t.Errorf("matchProbability(%q): expected positive probability, got %f", tt.input, result.probability)
		}
		if !tt.expectHit && result.probability > 0.5 {
			t.Errorf("matchProbability(%q): expected low probability, got %f", tt.input, result.probability)
		}
	}
}

func TestMaxSubsequence(t *testing.T) {
	values := []int{1, -1, 1, 1, -1, 1}
	avg, start, end := maxSubsequence(len(values), func(idx int) int {
		return values[idx]
	})

	if avg <= 0 {
		t.Errorf("expected positive average, got %f", avg)
	}
	if start > end {
		t.Errorf("start %d > end %d", start, end)
	}
}

func TestMaxSubsequenceEmpty(t *testing.T) {
	avg, start, end := maxSubsequence(0, func(idx int) int { return 0 })
	if avg != 0 || start != 0 || end != 0 {
		t.Errorf("expected all zeros for empty input, got avg=%f start=%d end=%d", avg, start, end)
	}
}
