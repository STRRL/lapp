package workspace

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var tmpl = template.Must(
	template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"join": func(sep string, items []string) string {
			return strings.Join(items, sep)
		},
	}).ParseFS(templateFS, "templates/*.tmpl"),
)

var errorPattern = regexp.MustCompile(`(?i)(error|warn|fatal|panic|exception|failed|timeout)`)
var validDirChar = regexp.MustCompile(`[^a-z0-9-]`)

// Builder creates the structured workspace directory from pre-processed data.
type Builder struct {
	dir       string
	tagged    []TaggedLine
	templates []pattern.DrainCluster
	labels    []semantic.SemanticLabel
	patterns  []PatternInfo
	unmatched []TaggedLine
	logFiles  []string
}

// NewBuilder creates a Builder with pre-processed data.
func NewBuilder(dir string, taggedLines []TaggedLine, templates []pattern.DrainCluster, labels []semantic.SemanticLabel) *Builder {
	return &Builder{
		dir:       dir,
		tagged:    taggedLines,
		templates: templates,
		labels:    labels,
	}
}

// BuildAll orchestrates writing all workspace files.
func (b *Builder) BuildAll() error {
	b.computePatterns()

	if err := b.writePatternDirs(); err != nil {
		return err
	}
	if err := b.writeUnmatched(); err != nil {
		return err
	}
	if err := b.writeNotes(); err != nil {
		return err
	}
	return b.writeAgentsMD()
}

func (b *Builder) computePatterns() {
	// Build label map
	labelMap := make(map[string]semantic.SemanticLabel, len(b.labels))
	for _, l := range b.labels {
		labelMap[l.PatternUUIDString] = l
	}

	// Collect log file names
	fileSet := make(map[string]bool)
	for _, tl := range b.tagged {
		fileSet[tl.FileName] = true
	}
	for f := range fileSet {
		b.logFiles = append(b.logFiles, f)
	}
	sort.Strings(b.logFiles)

	// Match each line to a template
	type lineWithTemplate struct {
		tagged     TaggedLine
		templateID string
	}
	matches := make([]lineWithTemplate, 0, len(b.tagged))
	for _, tl := range b.tagged {
		t, ok := pattern.MatchTemplate(tl.Content, b.templates)
		id := ""
		if ok {
			id = t.ID.String()
		}
		matches = append(matches, lineWithTemplate{tagged: tl, templateID: id})
	}

	// Build pattern info per template
	usedDirs := make(map[string]bool)
	infoMap := make(map[string]*PatternInfo)

	for _, t := range b.templates {
		tid := t.ID.String()
		label, hasLabel := labelMap[tid]
		if !hasLabel {
			continue
		}
		dirName := deduplicateDirName(usedDirs, sanitizeDirName(label.SemanticID))
		usedDirs[dirName] = true
		infoMap[tid] = &PatternInfo{
			SemanticID:  label.SemanticID,
			DirName:     dirName,
			Template:    t.Pattern,
			Description: label.Description,
		}
	}

	// Populate counts, samples, line refs
	for _, m := range matches {
		if m.templateID == "" {
			b.unmatched = append(b.unmatched, m.tagged)
			continue
		}
		info, ok := infoMap[m.templateID]
		if !ok {
			b.unmatched = append(b.unmatched, m.tagged)
			continue
		}
		info.Count++
		ref := LineRef{FileName: m.tagged.FileName, LineNum: m.tagged.LineNum}
		info.LineRefs = append(info.LineRefs, ref)
		if info.Count == 1 {
			info.FirstSeen = ref
		}
		info.LastSeen = ref
		if len(info.Samples) < 20 {
			info.Samples = append(info.Samples, m.tagged.Content)
		}
	}

	// Collect patterns sorted by count desc
	for _, info := range infoMap {
		b.patterns = append(b.patterns, *info)
	}
	sort.Slice(b.patterns, func(i, j int) bool {
		return b.patterns[i].Count > b.patterns[j].Count
	})
}

func (b *Builder) writePatternDirs() error {
	for _, p := range b.patterns {
		dir := filepath.Join(b.dir, "patterns", p.DirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errors.Errorf("create pattern dir %s: %w", p.DirName, err)
		}

		// pattern.md
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "pattern.md.tmpl", p); err != nil {
			return errors.Errorf("render pattern.md for %s: %w", p.DirName, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pattern.md"), buf.Bytes(), 0o644); err != nil {
			return err
		}

		// samples.log
		if err := os.WriteFile(filepath.Join(dir, "samples.log"), []byte(strings.Join(p.Samples, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) writeUnmatched() error {
	dir := filepath.Join(b.dir, "patterns", "unmatched")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var lines []string
	for _, tl := range b.unmatched {
		lines = append(lines, tl.Content)
	}

	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	return os.WriteFile(filepath.Join(dir, "samples.log"), []byte(content), 0o644)
}

func (b *Builder) writeNotes() error {
	notesDir := filepath.Join(b.dir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		return err
	}

	// summary.md
	summaryData := struct {
		FileCount      int
		LogFiles       []string
		TotalLines     int
		PatternCount   int
		UnmatchedCount int
		Patterns       []PatternInfo
	}{
		FileCount:      len(b.logFiles),
		LogFiles:       b.logFiles,
		TotalLines:     len(b.tagged),
		PatternCount:   len(b.patterns),
		UnmatchedCount: len(b.unmatched),
		Patterns:       b.patterns,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "summary.md.tmpl", summaryData); err != nil {
		return errors.Errorf("render summary.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "summary.md"), buf.Bytes(), 0o644); err != nil {
		return err
	}

	// errors.md
	var errorPatterns []PatternInfo
	for _, p := range b.patterns {
		if errorPattern.MatchString(p.Template) || errorPattern.MatchString(p.SemanticID) {
			errorPatterns = append(errorPatterns, p)
		}
	}

	var unmatchedErrors []string
	for _, tl := range b.unmatched {
		if errorPattern.MatchString(tl.Content) {
			unmatchedErrors = append(unmatchedErrors, tl.Content)
			if len(unmatchedErrors) >= 50 {
				break
			}
		}
	}

	errorsData := struct {
		ErrorPatterns   []PatternInfo
		UnmatchedErrors []string
		HasContent      bool
	}{
		ErrorPatterns:   errorPatterns,
		UnmatchedErrors: unmatchedErrors,
		HasContent:      len(errorPatterns) > 0 || len(unmatchedErrors) > 0,
	}
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "errors.md.tmpl", errorsData); err != nil {
		return errors.Errorf("render errors.md: %w", err)
	}
	return os.WriteFile(filepath.Join(notesDir, "errors.md"), buf.Bytes(), 0o644)
}

func (b *Builder) writeAgentsMD() error {
	data := struct {
		LogFiles []string
	}{
		LogFiles: b.logFiles,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "AGENTS.md.tmpl", data); err != nil {
		return errors.Errorf("render AGENTS.md: %w", err)
	}
	return os.WriteFile(filepath.Join(b.dir, "AGENTS.md"), buf.Bytes(), 0o644)
}

// deduplicateDirName appends -2, -3, etc. if the name is already taken.
// sanitizeDirName strips characters not in [a-z0-9-] to prevent path traversal.
func sanitizeDirName(name string) string {
	name = strings.ToLower(name)
	name = validDirChar.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "unknown"
	}
	return name
}

func deduplicateDirName(used map[string]bool, name string) string {
	if !used[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !used[candidate] {
			return candidate
		}
	}
}
