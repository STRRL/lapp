package analyzer

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/strrl/lapp/pkg/parser"
)

//go:embed AGENTS.md
var agentsMD []byte

var errorPattern = regexp.MustCompile(`(?i)(error|warn|fatal|panic|exception|failed|timeout)`)

type lineResult struct {
	raw        string
	templateID string
}

// BuildWorkspace creates pre-processed analysis files in the given directory.
func BuildWorkspace(dir string, lines []string, chain *parser.ChainParser) error {
	if err := writeRawLog(dir, lines); err != nil {
		return fmt.Errorf("write raw.log: %w", err)
	}

	// Parse all lines to discover templates
	var results []lineResult
	for _, line := range lines {
		r := chain.Parse(line)
		results = append(results, lineResult{
			raw:        line,
			templateID: r.TemplateID,
		})
	}

	templates := chain.Templates()
	if err := writeSummary(dir, templates, results); err != nil {
		return fmt.Errorf("write summary.txt: %w", err)
	}
	if err := writeErrors(dir, templates, results); err != nil {
		return fmt.Errorf("write errors.txt: %w", err)
	}
	if err := writeAgentsMD(dir); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
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

func writeSummary(dir string, templates []parser.Template, results []lineResult) error {
	// Count occurrences and collect samples per template
	type templateStats struct {
		id      string
		pattern string
		count   int
		samples []string
	}
	statsMap := make(map[string]*templateStats)

	for _, t := range templates {
		statsMap[t.ID] = &templateStats{
			id:      t.ID,
			pattern: t.Pattern,
		}
	}

	for _, r := range results {
		if r.templateID == "" {
			continue
		}
		s, ok := statsMap[r.templateID]
		if !ok {
			continue
		}
		s.count++
		if len(s.samples) < 3 {
			s.samples = append(s.samples, r.raw)
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

func writeErrors(dir string, templates []parser.Template, results []lineResult) error {
	// Find templates that match error patterns
	errorTemplates := make(map[string]bool)
	for _, t := range templates {
		if errorPattern.MatchString(t.Pattern) {
			errorTemplates[t.ID] = true
		}
	}

	// Also collect raw lines matching error patterns
	var buf strings.Builder
	buf.WriteString("# Error and Warning Patterns\n\n")

	// Error template section
	hasContent := false
	for _, t := range templates {
		if !errorTemplates[t.ID] {
			continue
		}
		hasContent = true
		count := 0
		var samples []string
		for _, r := range results {
			if r.templateID == t.ID {
				count++
				if len(samples) < 3 {
					samples = append(samples, r.raw)
				}
			}
		}
		fmt.Fprintf(&buf, "[%s] \"%s\" — %d occurrences\n", t.ID, t.Pattern, count)
		for i, sample := range samples {
			fmt.Fprintf(&buf, "  sample %d: %s\n", i+1, sample)
		}
		buf.WriteString("\n")
	}

	// Lines with error keywords but no template match
	var unmatchedErrors []string
	for _, r := range results {
		if r.templateID == "" && errorPattern.MatchString(r.raw) {
			unmatchedErrors = append(unmatchedErrors, r.raw)
			if len(unmatchedErrors) >= 50 {
				break
			}
		}
	}
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
