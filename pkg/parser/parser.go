package parser

import "strings"

// DrainCluster represents a discovered log template.
type DrainCluster struct {
	// FIXME: also use uuid type here
	ID      string
	Pattern string
	Count   int
}

// MatchTemplate finds the best matching template for a log line by comparing
// tokens against template patterns (where "<*>" is a wildcard).
// Returns the matched template and true, or zero-value and false if no match.
func MatchTemplate(line string, templates []DrainCluster) (DrainCluster, bool) {
	lineTokens := strings.Fields(line)
	for _, t := range templates {
		patTokens := strings.Fields(t.Pattern)
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
