package pattern

import (
	"strings"

	"github.com/google/uuid"
)

// DrainCluster represents a discovered log template.
type DrainCluster struct {
	ID      uuid.UUID
	Pattern string
	Count   int
}

// extraDelimiters must match the delimiters used in NewDrainParser's WithExtraDelimiter.
var extraDelimiters = []string{"|", "=", ","}

// tokenize splits a string using the same logic as Drain:
// replace extra delimiters with spaces, then split on spaces.
func tokenize(s string) []string {
	for _, d := range extraDelimiters {
		s = strings.ReplaceAll(s, d, " ")
	}
	return strings.Split(strings.TrimSpace(s), " ")
}

// MatchTemplate finds the best matching template for a log line by comparing
// tokens against template patterns (where "<*>" is a wildcard).
// Returns the matched template and true, or zero-value and false if no match.
func MatchTemplate(line string, templates []DrainCluster) (DrainCluster, bool) {
	lineTokens := tokenize(line)
	for _, t := range templates {
		patTokens := tokenize(t.Pattern)
		if matchTokens(lineTokens, patTokens) {
			return t, true
		}
	}
	return DrainCluster{}, false
}

func matchTokens(lineTokens, patTokens []string) bool {
	if len(lineTokens) != len(patTokens) {
		return false
	}
	for i, pt := range patTokens {
		if pt == "<*>" {
			continue
		}
		if pt != lineTokens[i] {
			return false
		}
	}
	return true
}
