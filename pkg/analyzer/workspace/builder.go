package workspace

import (
	"bytes"
	"embed"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/pattern"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(
	template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).ParseFS(templateFS, "templates/*.tmpl"),
)

var errorPattern = regexp.MustCompile(`(?i)(error|warn|fatal|panic|exception|failed|timeout)`)

type lineMatch struct {
	raw        string
	templateID string
}

// templateStats holds per-template statistics for rendering.
type templateStats struct {
	ID      string
	Pattern string
	Count   int
	Samples []string
}

// summaryData is the data passed to summary.txt.tmpl.
type summaryData struct {
	Stats []*templateStats
}

// errorsData is the data passed to errors.txt.tmpl.
type errorsData struct {
	ErrorTemplates  []*templateStats
	UnmatchedErrors []string
	HasContent      bool
}

// Builder prepares and writes workspace files for log analysis.
type Builder struct {
	dir       string
	lines     []string
	templates []pattern.DrainCluster
	matches   []lineMatch
}

// NewBuilder creates a Builder and pre-computes line-to-template matches.
func NewBuilder(dir string, lines []string, templates []pattern.DrainCluster) *Builder {
	matches := make([]lineMatch, 0, len(lines))
	for _, line := range lines {
		t, ok := pattern.MatchTemplate(line, templates)
		id := ""
		if ok {
			id = t.ID.String()
		}
		matches = append(matches, lineMatch{raw: line, templateID: id})
	}
	return &Builder{
		dir:       dir,
		lines:     lines,
		templates: templates,
		matches:   matches,
	}
}

// BuildAll writes all workspace files in order.
func (b *Builder) BuildAll() error {
	if err := b.WriteRawLog(); err != nil {
		return err
	}
	if err := b.WriteSummary(); err != nil {
		return err
	}
	if err := b.WriteErrors(); err != nil {
		return err
	}
	return b.WriteAgentsMD()
}

// WriteRawLog writes the raw log lines to raw.log.
func (b *Builder) WriteRawLog() error {
	return os.WriteFile(
		filepath.Join(b.dir, "raw.log"),
		[]byte(strings.Join(b.lines, "\n")),
		0o644,
	)
}

// WriteSummary writes template statistics to summary.txt.
func (b *Builder) WriteSummary() error {
	statsMap := make(map[string]*templateStats)

	for _, t := range b.templates {
		id := t.ID.String()
		statsMap[id] = &templateStats{
			ID:      id,
			Pattern: t.Pattern,
		}
	}

	for _, m := range b.matches {
		if m.templateID == "" {
			continue
		}
		s, ok := statsMap[m.templateID]
		if !ok {
			continue
		}
		s.Count++
		if len(s.Samples) < 3 {
			s.Samples = append(s.Samples, m.raw)
		}
	}

	var statsList []*templateStats
	for _, s := range statsMap {
		statsList = append(statsList, s)
	}
	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].Count > statsList[j].Count
	})

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "summary.txt.tmpl", summaryData{Stats: statsList}); err != nil {
		return errors.Errorf("render summary template: %w", err)
	}

	return os.WriteFile(filepath.Join(b.dir, "summary.txt"), buf.Bytes(), 0o644)
}

// WriteErrors writes error and warning patterns to errors.txt.
func (b *Builder) WriteErrors() error {
	errorTemplateIDs := make(map[string]bool)
	for _, t := range b.templates {
		if errorPattern.MatchString(t.Pattern) {
			errorTemplateIDs[t.ID.String()] = true
		}
	}

	var errTemplates []*templateStats
	for _, t := range b.templates {
		tid := t.ID.String()
		if !errorTemplateIDs[tid] {
			continue
		}
		count := 0
		var samples []string
		for _, m := range b.matches {
			if m.templateID == tid {
				count++
				if len(samples) < 3 {
					samples = append(samples, m.raw)
				}
			}
		}
		errTemplates = append(errTemplates, &templateStats{
			ID:      tid,
			Pattern: t.Pattern,
			Count:   count,
			Samples: samples,
		})
	}

	unmatchedErrors := b.collectUnmatchedErrors(50)

	hasContent := len(errTemplates) > 0 || len(unmatchedErrors) > 0

	var buf bytes.Buffer
	data := errorsData{
		ErrorTemplates:  errTemplates,
		UnmatchedErrors: unmatchedErrors,
		HasContent:      hasContent,
	}
	if err := templates.ExecuteTemplate(&buf, "errors.txt.tmpl", data); err != nil {
		return errors.Errorf("render errors template: %w", err)
	}

	return os.WriteFile(filepath.Join(b.dir, "errors.txt"), buf.Bytes(), 0o644)
}

// WriteAgentsMD writes the embedded AGENTS.md file.
func (b *Builder) WriteAgentsMD() error {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "AGENTS.md.tmpl", nil); err != nil {
		return errors.Errorf("render AGENTS.md template: %w", err)
	}
	return os.WriteFile(filepath.Join(b.dir, "AGENTS.md"), buf.Bytes(), 0o644)
}

func (b *Builder) collectUnmatchedErrors(limit int) []string {
	var errLines []string
	for _, m := range b.matches {
		if m.templateID == "" && errorPattern.MatchString(m.raw) {
			errLines = append(errLines, m.raw)
			if len(errLines) >= limit {
				break
			}
		}
	}
	return errLines
}
