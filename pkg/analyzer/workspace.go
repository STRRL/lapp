package analyzer

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/parser"
)

//go:embed AGENTS.md
var agentsMD []byte

var errorPattern = regexp.MustCompile(`(?i)(error|warn|fatal|panic|exception|failed|timeout)`)

// BuildWorkspace creates pre-processed analysis files in the given directory.
func BuildWorkspace(dir string, lines []string, templates []parser.DrainCluster) error {
	if err := writeRawLog(dir, lines); err != nil {
		return errors.Errorf("write raw.log: %w", err)
	}

	// Match each line against discovered templates
	var matches []lineMatch
	for _, line := range lines {
		t, ok := parser.MatchTemplate(line, templates)
		id := ""
		if ok {
			id = t.ID.String()
		}
		matches = append(matches, lineMatch{raw: line, templateID: id})
	}

	if err := writeSummary(dir, templates, matches); err != nil {
		return errors.Errorf("write summary.txt: %w", err)
	}
	if err := writeErrors(dir, templates, matches); err != nil {
		return errors.Errorf("write errors.txt: %w", err)
	}
	if err := writeAgentsMD(dir); err != nil {
		return errors.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

func writeAgentsMD(dir string) error {
	return os.WriteFile(filepath.Join(dir, "AGENTS.md"), agentsMD, 0o644)
}

func writeRawLog(dir string, lines []string) error {
	return os.WriteFile(
		filepath.Join(dir, "raw.log"),
		[]byte(strings.Join(lines, "\n")),
		0o644,
	)
}

type lineMatch struct {
	raw        string
	templateID string
}

func writeSummary(dir string, templates []parser.DrainCluster, matches []lineMatch) error {
	// Count occurrences and collect samples per template
	type templateStats struct {
		id      string
		pattern string
		count   int
		samples []string
	}
	statsMap := make(map[string]*templateStats)

	for _, t := range templates {
		id := t.ID.String()
		statsMap[id] = &templateStats{
			id:      id,
			pattern: t.Pattern,
		}
	}

	for _, m := range matches {
		if m.templateID == "" {
			continue
		}
		s, ok := statsMap[m.templateID]
		if !ok {
			continue
		}
		s.count++
		if len(s.samples) < 3 {
			s.samples = append(s.samples, m.raw)
		}
	}

	// Sort by count descending
	var statsList []*templateStats
	for _, s := range statsMap {
		statsList = append(statsList, s)
	}
	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].count > statsList[j].count
	})

	var buf strings.Builder
	buf.WriteString("# Log Template Summary\n\n")
	for _, s := range statsList {
		fmt.Fprintf(&buf, "[%s] \"%s\" — %d occurrences\n", s.id, s.pattern, s.count)
		for i, sample := range s.samples {
			fmt.Fprintf(&buf, "  sample %d: %s\n", i+1, sample)
		}
		buf.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(buf.String()), 0o644)
}

func writeErrors(dir string, templates []parser.DrainCluster, matches []lineMatch) error {
	// Find templates that match error patterns
	errorTemplates := make(map[string]bool)
	for _, t := range templates {
		if errorPattern.MatchString(t.Pattern) {
			errorTemplates[t.ID.String()] = true
		}
	}

	var buf strings.Builder
	buf.WriteString("# Error and Warning Patterns\n\n")

	hasContent := writeErrorTemplates(&buf, templates, errorTemplates, matches)

	// Lines with error keywords but no template match
	unmatchedErrors := collectUnmatchedErrors(matches, 50)
	if len(unmatchedErrors) > 0 {
		hasContent = true
		buf.WriteString("## Unmatched Error Lines\n\n")
		for _, line := range unmatchedErrors {
			fmt.Fprintf(&buf, "  %s\n", line)
		}
	}

	if !hasContent {
		buf.WriteString("No error or warning patterns detected.\n")
	}

	return os.WriteFile(filepath.Join(dir, "errors.txt"), []byte(buf.String()), 0o644)
}

func writeErrorTemplates(buf *strings.Builder, templates []parser.DrainCluster, errorTemplates map[string]bool, matches []lineMatch) bool {
	hasContent := false
	for _, t := range templates {
		tid := t.ID.String()
		if !errorTemplates[tid] {
			continue
		}
		hasContent = true
		count := 0
		var samples []string
		for _, m := range matches {
			if m.templateID == tid {
				count++
				if len(samples) < 3 {
					samples = append(samples, m.raw)
				}
			}
		}
		fmt.Fprintf(buf, "[%s] \"%s\" — %d occurrences\n", tid, t.Pattern, count)
		for i, sample := range samples {
			fmt.Fprintf(buf, "  sample %d: %s\n", i+1, sample)
		}
		buf.WriteString("\n")
	}
	return hasContent
}

func collectUnmatchedErrors(matches []lineMatch, limit int) []string {
	var errLines []string
	for _, m := range matches {
		if m.templateID == "" && errorPattern.MatchString(m.raw) {
			errLines = append(errLines, m.raw)
			if len(errLines) >= limit {
				break
			}
		}
	}
	return errLines
}
