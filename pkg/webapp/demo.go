package webapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
	llmconfig "github.com/strrl/lapp/pkg/config"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/workspace"
)

// The demo API is plain JSON over HTTP: it backs the "find patterns, filter
// noise" pages (import + pattern list) without touching the proto service.

const (
	maxImportBytes  = 200 << 20
	maxDetailLines  = 2000
	maxBuckets      = 72
	noiseMinShare   = 0.10
	noiseMinPresent = 0.5
)

var leadingLevel = regexp.MustCompile(`(?i)^\[?(?:fatal|panic|crit(?:ical)?|err(?:or)?|warn(?:ing)?|notice|info|debug|trace)\]?:?\s+`)

var levelPattern = regexp.MustCompile(`(?i)\b(fatal|panic|emerg|alert|crit(?:ical)?|err(?:or)?|warn(?:ing)?|notice|info|debug|trace)\b`)

type demoAPI struct {
	root     string
	rulesDir string
	apiKey   string
	model    string
	fetcher  workspace.ImportFetcher
	jobs     *investigations
}

func newDemoAPI(root, apiKey, model string) *demoAPI {
	return &demoAPI{root: root, rulesDir: filepath.Join(filepath.Dir(root), "noise-rules"), apiKey: apiKey, model: model,
		jobs: &investigations{jobs: map[string]*investigation{}}}
}

type demoWorkspace struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Service       string `json:"service"`
	LineCount     int    `json:"lineCount"`
	PatternCount  int    `json:"patternCount"`
	FilteredLines int    `json:"filteredLines"`
	LimitReached  bool   `json:"limitReached,omitempty"`
	// TruncatedLines counts lines cut at logsource.MaxLineBytes.
	TruncatedLines int       `json:"truncatedLines,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type demoPattern struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Named       bool     `json:"named"`
	Description string   `json:"description,omitempty"`
	Template    string   `json:"template"`
	Count       int      `json:"count"`
	Level       string   `json:"level"`
	Buckets     []int    `json:"buckets"`
	Suggestion  string   `json:"suggestion"` // "noise", "maybe" or ""
	Reason      string   `json:"reason"`
	KeptReason  string   `json:"keptReason,omitempty"`
	Filtered    bool     `json:"filtered"`
	SpikeAt     *int     `json:"spikeAt,omitempty"`
	NewAt       *int     `json:"newAt,omitempty"`
	Samples     []string `json:"samples"`
	SinceLast   string   `json:"sinceLast,omitempty"` // new, up, down
	Ratio       float64  `json:"ratio,omitempty"`     // lines per hour vs last time
	PrevCount   *int     `json:"prevCount,omitempty"`
}

type demoUnmatched struct {
	Count   int      `json:"count"`
	Buckets []int    `json:"buckets"`
	Samples []string `json:"samples"`
}

type demoPatternsResponse struct {
	Workspace      demoWorkspace `json:"workspace"`
	FileNames      []string      `json:"fileNames"`
	From           *time.Time    `json:"from,omitempty"`
	To             *time.Time    `json:"to,omitempty"`
	BucketStart    *time.Time    `json:"bucketStart,omitempty"`
	BucketSeconds  int64         `json:"bucketSeconds"`
	RulesSaved     bool          `json:"rulesSaved"`
	RulesApplied   int           `json:"rulesApplied"`
	Named          bool          `json:"named"`
	KeySet         bool          `json:"keySet"`
	NamingApproved bool          `json:"namingApproved"`
	Patterns       []demoPattern `json:"patterns"`
	Unmatched      demoUnmatched `json:"unmatched"`
	Compare        *demoCompare  `json:"compare,omitempty"`
}

type noiseRules struct {
	Filtered []string `json:"filtered"`
	Shown    []string `json:"shown"`
}

func (d *demoAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/demo/workspaces", d.handleWorkspaces)
	mux.HandleFunc("POST /api/demo/import", d.handleImport)
	mux.HandleFunc("GET /api/demo/patterns", d.handlePatterns)
	mux.HandleFunc("POST /api/demo/rules", d.handleRules)
	mux.HandleFunc("GET /api/demo/status", d.handleStatus)
	mux.HandleFunc("GET /api/demo/service-rules", d.handleServiceRules)
	mux.HandleFunc("POST /api/demo/service", d.handleRenameService)
	mux.HandleFunc("POST /api/demo/import-gcp", d.handleImportGCP)
	mux.HandleFunc("GET /api/demo/gcp-recent", d.handleGCPRecent)
	mux.HandleFunc("POST /api/demo/investigate", d.handleInvestigateStart)
	mux.HandleFunc("GET /api/demo/investigate", d.handleInvestigateGet)
	mux.HandleFunc("POST /api/demo/investigate/stop", d.handleInvestigateStop)
	mux.HandleFunc("GET /api/demo/agents", d.handleAgents)
	mux.HandleFunc("GET /api/demo/llm-preview", d.handleLLMPreview)
	mux.HandleFunc("POST /api/demo/name", d.handleName)
	mux.HandleFunc("GET /api/demo/lines", d.handleLines)
	mux.HandleFunc("GET /api/demo/pattern", d.handlePatternDetail)
	mux.HandleFunc("GET /api/demo/export", d.handleExport)
}

func (d *demoAPI) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(d.root)
	if err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := []demoWorkspace{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(d.root, e.Name())
		records, err := workspace.ListDiscoveryRunRecords(dir)
		if err != nil {
			continue
		}
		run := workspace.LatestSuccessfulDiscoveryRun(records)
		if run == nil {
			continue
		}
		info := d.workspaceInfo(e.Name(), run)
		if len(out) < 20 {
			if a, err := d.analyze(r.Context(), e.Name()); err == nil {
				for _, p := range a.resp.Patterns {
					if p.Filtered {
						info.FilteredLines += p.Count
					}
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	writeJSON(w, out)
}

func (d *demoAPI) workspaceInfo(id string, run *workspace.DiscoveryRunRecord) demoWorkspace {
	dir := filepath.Join(d.root, id)
	info := demoWorkspace{ID: id, Title: id, Service: readService(dir, id), LineCount: run.LineCount, PatternCount: run.PatternCount, TruncatedLines: run.TruncatedLineCount}
	if files, err := workspace.ListLogFiles(dir); err == nil && len(files) > 0 {
		info.Title = files[0]
	}
	if rec := latestImport(dir); rec != nil {
		info.Title = describeImport(rec)
		info.LimitReached = rec.Truncated
	}
	if run.FinishedAt != nil {
		info.UpdatedAt = *run.FinishedAt
	}
	return info
}

// handleImport lands a raw log file body in a new workspace and starts
// finding patterns in the background; poll /api/demo/status for progress.
func (d *demoAPI) handleImport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, dir, service, err := d.landNew(q.Get("name"), q.Get("service"), http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runID, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, RunID: runID.String(), APIKey: d.namingKey(service), Model: d.model, Mask: d.namingMask(service)}); err != nil {
			slog.Error("import failed", "workspace", id, "error", err)
		}
	}()
	writeJSON(w, map[string]string{"id": id, "service": service})
}

// importLog lands a log and finds its patterns before returning (CLI path).
func (d *demoAPI) importLog(ctx context.Context, name string, body io.Reader) (string, error) {
	id, dir, svc, err := d.landNew(name, "", body)
	if err != nil {
		return "", err
	}
	if _, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, APIKey: d.namingKey(svc), Model: d.model, Mask: d.namingMask(svc)}); err != nil {
		return "", err
	}
	return id, nil
}

func (d *demoAPI) landNew(name, service string, body io.Reader) (id, dir, svc string, err error) {
	name = filepath.Base(name)
	if name == "" || name == "." || name == "/" {
		name = "stdin.log"
	}
	svc = serviceFromFileName(name)
	if strings.TrimSpace(service) != "" {
		svc = cleanServiceName(service)
	}
	id, dir, err = d.newWorkspaceDir(svc)
	if err != nil {
		return "", "", "", err
	}
	if err := writeLog(dir, name, svc, body); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", "", err
	}
	return id, dir, svc, nil
}

func (d *demoAPI) landLog(ctx context.Context, dir, name, service string, body io.Reader) error {
	if err := writeLog(dir, name, service, body); err != nil {
		return err
	}
	_, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, APIKey: d.apiKey, Model: d.model})
	return err
}

func writeLog(dir, name, service string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "logs", name))
	if err != nil {
		return err
	}
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "service"), []byte(service), 0o644)
}

type demoStatus struct {
	State string `json:"state"` // queued, running, succeeded, failed
	Step  string `json:"step"`
	Error string `json:"error,omitempty"`
	// ErrorCode is the run's structured error code, e.g. FORMAT_UNRECOGNIZED.
	ErrorCode string `json:"errorCode,omitempty"`
	Named     bool   `json:"named"`
	Lines     int    `json:"lines"`
	Batches   string `json:"batches,omitempty"`
	Source    string `json:"source,omitempty"`
}

func (d *demoAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ws")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, errors.New("invalid workspace"))
		return
	}
	st := demoStatus{State: "queued", Named: d.apiKey != ""}
	dir := filepath.Join(d.root, id)
	if rec := latestImport(dir); rec != nil {
		st.Source = "gcp"
		switch rec.State {
		case workspace.ImportRunStateFailed:
			st.State, st.Step = "failed", "IMPORTING"
			if rec.Error != nil {
				st.Error, st.ErrorCode = rec.Error.Message, rec.Error.Code
			}
			writeJSON(w, st)
			return
		case workspace.ImportRunStateSucceeded:
			st.Lines = rec.EntryCount
			if rec.EntryCount == 0 {
				st.State, st.Step, st.Error = "failed", "IMPORTING", "No log entries matched this project, filter and time range."
				writeJSON(w, st)
				return
			}
		default:
			st.State, st.Step, st.Lines = "running", "IMPORTING", rec.EntryCount
			writeJSON(w, st)
			return
		}
	}
	records, _ := workspace.ListDiscoveryRunRecords(dir)
	if run := workspace.LatestDiscoveryRun(records); run != nil {
		st.State = strings.ToLower(string(run.State))
		st.Step = string(run.CurrentStep)
		if run.LineCount > 0 {
			st.Lines = run.LineCount
		}
		if run.Error != nil {
			st.Error, st.ErrorCode = run.Error.Message, run.Error.Code
		}
		if p := run.Progress; p != nil && p.LabelBatch != nil {
			st.Batches = fmt.Sprintf("batch %d of %d", p.LabelBatch.BatchNumber, p.LabelBatch.BatchCount)
		}
	}
	writeJSON(w, st)
}

func (d *demoAPI) handleServiceRules(w http.ResponseWriter, r *http.Request) {
	rules, ok := d.loadRules(cleanServiceName(r.URL.Query().Get("service")))
	n := 0
	if ok {
		n = len(rules.Filtered)
	}
	writeJSON(w, map[string]int{"count": n})
}

// handleRenameService changes which service a workspace's noise rules are
// saved under (e.g. naming a piped "stdin" import).
func (d *demoAPI) handleRenameService(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ws")
	name := cleanServiceName(r.URL.Query().Get("name"))
	if !validID(id) || strings.TrimSpace(r.URL.Query().Get("name")) == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid workspace or name"))
		return
	}
	dir := filepath.Join(d.root, id)
	if _, err := os.Stat(dir); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "service"), []byte(name), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"service": name})
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.HasPrefix(id, ".")
}

func (d *demoAPI) newWorkspaceDir(service string) (string, string, error) {
	base := service
	for i := 1; i < 1000; i++ {
		id := base
		if i > 1 {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		dir := filepath.Join(d.root, id)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return id, dir, os.MkdirAll(dir, 0o755)
		}
	}
	return "", "", errors.New("too many workspaces for " + service)
}

// analysis is everything the pattern list, detail and export views need.
type analysis struct {
	resp    demoPatternsResponse
	lines   []workspace.TaggedLine
	owner   []int // pattern index per line, -1 for unmatched
	service string
}

func (d *demoAPI) analyze(ctx context.Context, id string) (*analysis, error) {
	return d.analyzeWith(ctx, id, "")
}

// analyzeWith is analyze with a chosen comparison baseline: "" picks the
// latest earlier import of the same service, "none" skips comparison.
func (d *demoAPI) analyzeWith(ctx context.Context, id, compareTo string) (*analysis, error) {
	if !validID(id) {
		return nil, errors.New("invalid workspace")
	}
	dir := filepath.Join(d.root, id)
	records, err := workspace.ListDiscoveryRunRecords(dir)
	if err != nil {
		return nil, err
	}
	run := workspace.LatestSuccessfulDiscoveryRun(records)
	if run == nil {
		return nil, errors.New("no patterns yet for " + id)
	}
	lines, err := workspace.LoadTaggedLines(ctx, dir)
	if err != nil {
		return nil, err
	}
	a := &analysis{service: readService(dir, id)}
	a.resp.Workspace = d.workspaceInfo(id, run)
	a.resp.FileNames, _ = workspace.ListLogFiles(dir)

	// Re-match lines against the run's templates so counts, buckets and
	// export always agree with each other.
	templates := make([]pattern.DrainCluster, len(run.Patterns))
	for i, p := range run.Patterns {
		templates[i] = pattern.DrainCluster{Pattern: p.Template}
		templates[i].ID[0], templates[i].ID[1] = byte(i>>8), byte(i)
	}
	a.owner = make([]int, len(lines))
	counts := make([]int, len(templates))
	var from, to time.Time
	for i, tl := range lines {
		a.owner[i] = -1
		if t, ok := pattern.MatchTemplate(tl.DrainLine(), templates); ok {
			idx := int(t.ID[0])<<8 | int(t.ID[1])
			a.owner[i] = idx
			counts[idx]++
		}
		if tl.Timestamp != nil {
			if from.IsZero() || tl.Timestamp.Before(from) {
				from = *tl.Timestamp
			}
			if tl.Timestamp.After(to) {
				to = *tl.Timestamp
			}
		}
	}
	a.lines = lines

	bucketSeconds, bucketCount := int64(0), 0
	var bucketStart time.Time
	if !from.IsZero() {
		bucketSeconds = chooseBucket(to.Sub(from))
		bucketStart = time.Unix(from.Unix()-from.Unix()%bucketSeconds, 0).UTC()
		bucketCount = int(to.Unix()-bucketStart.Unix())/int(bucketSeconds) + 1
		a.resp.From, a.resp.To, a.resp.BucketStart = &from, &to, &bucketStart
	}
	a.resp.BucketSeconds = bucketSeconds

	rules, rulesSaved := d.loadRules(a.service)
	a.resp.RulesSaved = rulesSaved
	a.resp.KeySet = d.apiKey != ""
	a.resp.NamingApproved = d.namingApproved(a.service)
	for _, p := range run.Patterns {
		if p.SemanticID != "" && p.SemanticID != p.Template {
			a.resp.Named = true
			break
		}
	}

	total := len(lines)
	patterns := make([]demoPattern, len(run.Patterns))
	for i, p := range run.Patterns {
		named := p.SemanticID != "" && p.SemanticID != p.Template
		name := p.SemanticID
		if !named {
			// The Level column already shows "[error]"-style markers.
			name = leadingLevel.ReplaceAllString(p.Template, "")
		}
		patterns[i] = demoPattern{
			ID:    p.Template,
			Name:  name,
			Named: named,
			Description: func() string {
				if named {
					return p.Description
				}
				return ""
			}(),
			Template: p.Template,
			Count:    counts[i],
			Level:    detectLevel(p.Template),
			Buckets:  make([]int, bucketCount),
			Samples:  []string{},
		}
	}
	unmatched := demoUnmatched{Buckets: make([]int, bucketCount), Samples: []string{}}
	for i, tl := range lines {
		b := -1
		if tl.Timestamp != nil && bucketCount > 0 {
			b = int(tl.Timestamp.Unix()-bucketStart.Unix()) / int(bucketSeconds)
		}
		if o := a.owner[i]; o >= 0 {
			if b >= 0 {
				patterns[o].Buckets[b]++
			}
			if len(patterns[o].Samples) < 5 {
				patterns[o].Samples = append(patterns[o].Samples, tl.Content)
			}
			continue
		}
		unmatched.Count++
		if b >= 0 {
			unmatched.Buckets[b]++
		}
		if len(unmatched.Samples) < 200 {
			unmatched.Samples = append(unmatched.Samples, tl.Content)
		}
	}
	for i := range patterns {
		p := &patterns[i]
		suggestNoise(p, total, bucketCount, bucketUnit(bucketSeconds))
		markTrends(p)
		p.Filtered = p.Suggestion == "noise"
		if rulesSaved {
			p.Filtered = contains(rules.Filtered, p.ID) || (p.Filtered && !contains(rules.Shown, p.ID))
			if contains(rules.Filtered, p.ID) {
				a.resp.RulesApplied++
			}
		}
	}
	sort.SliceStable(patterns, func(i, j int) bool { return patterns[i].Count > patterns[j].Count })
	// Keep owner indices valid after sorting.
	pos := map[string]int{}
	for i, p := range patterns {
		pos[p.ID] = i
	}
	for i, o := range a.owner {
		if o >= 0 {
			a.owner[i] = pos[run.Patterns[o].Template]
		}
	}
	a.resp.Patterns = patterns
	a.resp.Unmatched = unmatched
	if compareTo != "none" {
		a.resp.Compare = d.compareWithPrevious(ctx, id, compareTo, a.service, run, &a.resp)
	}
	return a, nil
}

type gonePattern struct {
	Template string `json:"template"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
}

type compareOption struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
}

type demoCompare struct {
	PreviousID    string          `json:"previousId"`
	PreviousTitle string          `json:"previousTitle"`
	PreviousAt    time.Time       `json:"previousAt"`
	New           int             `json:"new"`
	Changed       int             `json:"changed"`
	Gone          []gonePattern   `json:"gone"`
	Options       []compareOption `json:"options"`
	ByRate        bool            `json:"byRate"` // false: compared by share of lines (no timestamps)
}

// compareWithPrevious compares this import with an earlier import of the
// same service (the latest one unless compareTo names one). Patterns are
// matched by template text; volume is compared in lines per hour (or share
// of lines when either log has no timestamps) and only ratios of ≥2 or ≤0.5
// count as a change.
func (d *demoAPI) compareWithPrevious(ctx context.Context, id, compareTo, service string, run *workspace.DiscoveryRunRecord, resp *demoPatternsResponse) *demoCompare {
	if run.FinishedAt == nil {
		return nil
	}
	entries, _ := os.ReadDir(d.root)
	var options []compareOption
	for _, e := range entries {
		if !e.IsDir() || e.Name() == id {
			continue
		}
		dir := filepath.Join(d.root, e.Name())
		if readService(dir, e.Name()) != service {
			continue
		}
		records, err := workspace.ListDiscoveryRunRecords(dir)
		if err != nil {
			continue
		}
		r := workspace.LatestSuccessfulDiscoveryRun(records)
		if r == nil || r.FinishedAt == nil || !r.FinishedAt.Before(*run.FinishedAt) {
			continue
		}
		options = append(options, compareOption{ID: e.Name(), Title: d.workspaceInfo(e.Name(), r).Title, At: *r.FinishedAt})
	}
	if len(options) == 0 {
		return nil
	}
	sort.Slice(options, func(i, j int) bool { return options[i].At.After(options[j].At) })
	chosen := options[0]
	for _, o := range options {
		if o.ID == compareTo {
			chosen = o
		}
	}
	prev, err := d.analyzeWith(ctx, chosen.ID, "none")
	if err != nil {
		return nil
	}
	out := &demoCompare{PreviousID: chosen.ID, PreviousTitle: chosen.Title, PreviousAt: chosen.At, Gone: []gonePattern{}, Options: options}

	hours := func(r demoPatternsResponse) float64 {
		if r.From == nil || r.To == nil {
			return 0
		}
		return math.Max(r.To.Sub(*r.From).Hours(), 1)
	}
	curH, prevH := hours(*resp), hours(prev.resp)
	out.ByRate = curH > 0 && prevH > 0
	rate := func(count int, r demoPatternsResponse, h float64) float64 {
		if out.ByRate {
			return float64(count) / h
		}
		return float64(count) / float64(max(r.Workspace.LineCount, 1))
	}
	before := map[string]int{}
	for _, p := range prev.resp.Patterns {
		before[p.ID] = p.Count
	}
	for i := range resp.Patterns {
		p := &resp.Patterns[i]
		was, ok := before[p.ID]
		if !ok {
			p.SinceLast = "new"
			out.New++
			continue
		}
		delete(before, p.ID)
		if p.Count < 5 && was < 5 {
			continue
		}
		ratio := rate(p.Count, *resp, curH) / math.Max(rate(was, prev.resp, prevH), 1e-9)
		p.PrevCount = &was
		switch {
		case ratio >= 2:
			p.SinceLast, p.Ratio = "up", round1(ratio)
			out.Changed++
		case ratio <= 0.5:
			p.SinceLast, p.Ratio = "down", round1(ratio)
			out.Changed++
		}
	}
	for _, p := range prev.resp.Patterns {
		if _, gone := before[p.ID]; gone {
			out.Gone = append(out.Gone, gonePattern{Template: p.ID, Name: p.Name, Count: p.Count})
		}
	}
	return out
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func chooseBucket(span time.Duration) int64 {
	for _, s := range []int64{60, 300, 900, 3600, 6 * 3600, 24 * 3600, 7 * 24 * 3600} {
		if int64(span.Seconds())/s < maxBuckets {
			return s
		}
	}
	return 30 * 24 * 3600
}

// suggestNoise flags high-volume, steady, non-error patterns as likely noise.
// High-volume error-looking patterns are only "maybe": the user decides.
func suggestNoise(p *demoPattern, total, bucketCount int, unit string) {
	if total == 0 {
		return
	}
	share := float64(p.Count) / float64(total)
	if share < noiseMinShare {
		return
	}
	present := 0
	for _, c := range p.Buckets {
		if c > 0 {
			present++
		}
	}
	steady := bucketCount == 0 || float64(present)/float64(bucketCount) >= noiseMinPresent
	parts := []string{formatPercent(share)}
	switch {
	case bucketCount == 0:
	case present == bucketCount:
		parts = append(parts, "every "+unit)
	default:
		parts = append(parts, fmt.Sprintf("in %d of %d %s", present, bucketCount, pluralUnit(unit)))
	}
	switch {
	case isErrorLike(p.Template):
		p.Suggestion, p.KeptReason = "maybe", "looks like an error"
	case !steady:
		p.Suggestion, p.KeptReason = "maybe", "comes in bursts"
	default:
		p.Suggestion = "noise"
	}
	p.Reason = strings.Join(parts, " · ")
}

func pluralUnit(unit string) string {
	if f := strings.Fields(unit); len(f) == 2 {
		// "5 minutes" -> "5-minute slots"
		return f[0] + "-" + strings.TrimSuffix(f[1], "s") + " slots"
	}
	return unit + "s"
}

func bucketUnit(seconds int64) string {
	switch seconds {
	case 60:
		return "minute"
	case 300:
		return "5 minutes"
	case 900:
		return "15 minutes"
	case 3600:
		return "hour"
	case 6 * 3600:
		return "6 hours"
	case 24 * 3600:
		return "day"
	case 7 * 24 * 3600:
		return "week"
	}
	return "month"
}

func markTrends(p *demoPattern) {
	if len(p.Buckets) < 4 {
		return
	}
	first, peak, sum, nonzero := -1, 0, 0, 0
	for i, c := range p.Buckets {
		if c > 0 {
			if first < 0 {
				first = i
			}
			sum += c
			nonzero++
		}
		if c > p.Buckets[peak] {
			peak = i
		}
	}
	if first > len(p.Buckets)/4 {
		p.NewAt = &first
	}
	if nonzero > 1 {
		mean := float64(sum) / float64(nonzero)
		if p.Buckets[peak] >= 10 && float64(p.Buckets[peak]) >= 3*mean {
			p.SpikeAt = &peak
		}
	}
}

func detectLevel(template string) string {
	head := template
	if len(head) > 48 {
		head = head[:48]
	}
	m := levelPattern.FindString(head)
	switch strings.ToLower(m) {
	case "fatal", "panic", "emerg", "alert", "crit", "critical":
		return "fatal"
	case "err", "error":
		return "error"
	case "warn", "warning":
		return "warn"
	case "notice":
		return "notice"
	case "info":
		return "info"
	case "debug", "trace":
		return "debug"
	}
	if isErrorLike(template) {
		return "error"
	}
	return ""
}

var failureLike = regexp.MustCompile(`(?i)\b(failure|denied|refused|abnormal)`)

func isErrorLike(s string) bool {
	return errorLikePattern.MatchString(s) || failureLike.MatchString(s)
}

// formatPercent is the one place percentages are rounded (one decimal,
// half away from zero) so every view shows the same number.
func formatPercent(share float64) string {
	return fmt.Sprintf("%.1f%%", math.Round(share*1000)/10)
}

func (d *demoAPI) handlePatterns(w http.ResponseWriter, r *http.Request) {
	a, err := d.analyzeWith(r.Context(), r.URL.Query().Get("ws"), r.URL.Query().Get("compare"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, a.resp)
}

func (d *demoAPI) handleRules(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ws")
	a, err := d.analyze(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body struct {
		Filtered []string `json:"filtered"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rules, _ := d.loadRules(a.service)
	rules.Filtered = without(rules.Filtered, a.resp.Patterns)
	rules.Shown = without(rules.Shown, a.resp.Patterns)
	for _, p := range a.resp.Patterns {
		if contains(body.Filtered, p.ID) {
			rules.Filtered = append(rules.Filtered, p.ID)
		} else if p.Suggestion == "noise" {
			rules.Shown = append(rules.Shown, p.ID)
		}
	}
	if err := d.saveRules(a.service, rules); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"service": a.service})
}

type demoLine struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

func (d *demoAPI) handleLines(w http.ResponseWriter, r *http.Request) {
	a, err := d.analyze(r.Context(), r.URL.Query().Get("ws"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	want := r.URL.Query().Get("pattern")
	out := []demoLine{}
	for i, tl := range a.lines {
		o := a.owner[i]
		if (want == "" && o < 0) || (o >= 0 && a.resp.Patterns[o].ID == want) {
			out = append(out, demoLine{N: tl.LineNum, Text: tl.Content})
			if len(out) >= maxDetailLines {
				break
			}
		}
	}
	writeJSON(w, out)
}

func (d *demoAPI) handleExport(w http.ResponseWriter, r *http.Request) {
	a, err := d.analyze(r.Context(), r.URL.Query().Get("ws"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var override []string
	if q, ok := r.URL.Query()["filtered"]; ok {
		override = q
		if override == nil {
			override = []string{}
		}
	}
	name := strings.TrimSuffix(a.resp.Workspace.Title, filepath.Ext(a.resp.Workspace.Title)) + ".filtered.log"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_ = a.writeRemaining(w, override)
}

// writeRemaining writes the original lines minus filtered patterns. A non-nil
// override replaces the saved/suggested filter set (current page state).
func (a *analysis) writeRemaining(w io.Writer, override []string) error {
	filtered := make([]bool, len(a.resp.Patterns))
	for i, p := range a.resp.Patterns {
		filtered[i] = p.Filtered
		if override != nil {
			filtered[i] = contains(override, p.ID)
		}
	}
	bw := bufio.NewWriter(w)
	for i, tl := range a.lines {
		if o := a.owner[i]; o >= 0 && filtered[o] {
			continue
		}
		if _, err := bw.WriteString(tl.Content + "\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func (d *demoAPI) rulesPath(service string) string {
	return filepath.Join(d.rulesDir, service+".json")
}

func (d *demoAPI) loadRules(service string) (noiseRules, bool) {
	var rules noiseRules
	data, err := os.ReadFile(d.rulesPath(service))
	if err != nil {
		return rules, false
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		slog.Warn("bad noise rules file", "service", service, "error", err)
		return noiseRules{}, false
	}
	return rules, true
}

func (d *demoAPI) saveRules(service string, rules noiseRules) error {
	path := d.rulesPath(service)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

var serviceChars = regexp.MustCompile(`[^a-z0-9]+`)

// serviceFromFileName derives the rule scope from a log file name:
// "Apache_2k.log" and "apache-2024-10-20.log" both become "apache".
func serviceFromFileName(name string) string {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	stem = serviceChars.ReplaceAllString(stem, "-")
	parts := strings.Split(strings.Trim(stem, "-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return "logs"
	}
	return parts[0]
}

// cleanServiceName keeps a user-chosen service name whole ("checkout-api"),
// only normalizing case and separators.
func cleanServiceName(name string) string {
	n := strings.Trim(serviceChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	if n == "" {
		return "logs"
	}
	return n
}

func readService(dir, fallback string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "service")); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	return serviceFromFileName(fallback)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// without drops entries that refer to patterns in this workspace, so a save
// replaces this workspace's decisions but keeps rules for other patterns.
func without(list []string, patterns []demoPattern) []string {
	out := list[:0:0]
	for _, v := range list {
		keep := true
		for _, p := range patterns {
			if p.ID == v {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, v)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// ImportLog lands a log in a new workspace under root and finds its patterns.
// It returns the workspace id to open in the web app.
func ImportLog(ctx context.Context, root, apiKey, model, name string, body io.Reader) (string, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return "", err
	}
	return newDemoAPI(root, apiKey, model).importLog(ctx, name, body)
}

// FilterLog finds patterns in a log without an LLM, applies the saved (or
// suggested) noise rules for its service, and writes the remaining lines.
// Nothing is kept: the scratch workspace is removed afterwards.
func FilterLog(ctx context.Context, root, service, name string, body io.Reader, out io.Writer) error {
	root, err := resolveRoot(root)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "lapp-filter-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	d := newDemoAPI(root, "", "")
	if service == "" {
		service = serviceFromFileName(filepath.Base(name))
	} else {
		service = cleanServiceName(service)
	}
	dir := filepath.Join(scratch, "ws")
	if err := d.landLog(ctx, dir, filepath.Base(name), service, body); err != nil {
		return err
	}
	scratchAPI := &demoAPI{root: scratch, rulesDir: d.rulesDir}
	a, err := scratchAPI.analyze(ctx, "ws")
	if err != nil {
		return err
	}
	return a.writeRemaining(out, nil)
}

func resolveRoot(root string) (string, error) {
	if root != "" {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".lapp", "workspaces"), nil
}

type fieldStat struct {
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
}

type valueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type demoField struct {
	Index     int          `json:"index"` // 1-based position among the template's <*>
	Kind      string       `json:"kind"`  // "number" or "text"
	Unit      string       `json:"unit,omitempty"`
	Buckets   []*fieldStat `json:"buckets,omitempty"`
	Overall   *fieldStat   `json:"overall,omitempty"`
	Breakdown []valueCount `json:"breakdown,omitempty"`
	Other     int          `json:"other,omitempty"`
}

// fieldSummary describes one <*> for the hover popover, including fields
// the charts skip (a single value, or a different value on every line).
type fieldSummary struct {
	Index    int          `json:"index"`
	Kind     string       `json:"kind"` // same, values, numbers, distinct
	Lines    int          `json:"lines"`
	Distinct int          `json:"distinct"`
	Top      []valueCount `json:"top"`
	Overall  *fieldStat   `json:"overall,omitempty"`
	Unit     string       `json:"unit,omitempty"`
}

type demoPatternDetail struct {
	FirstSeen *time.Time     `json:"firstSeen,omitempty"`
	LastSeen  *time.Time     `json:"lastSeen,omitempty"`
	Fields    []demoField    `json:"fields"`
	Summary   []fieldSummary `json:"summary"`
}

var numberValue = regexp.MustCompile(`^-?\d+(?:\.\d+)?([a-zA-Zµ%]*)$`)

func fieldTokens(s string) []string {
	for _, d := range []string{"|", "=", ","} {
		s = strings.ReplaceAll(s, d, " ")
	}
	return strings.Split(strings.TrimSpace(s), " ")
}

// handlePatternDetail computes per-field metrics for one pattern: number
// fields get median/p95/max per time slot, text fields a value breakdown.
func (d *demoAPI) handlePatternDetail(w http.ResponseWriter, r *http.Request) {
	a, err := d.analyze(r.Context(), r.URL.Query().Get("ws"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	want := r.URL.Query().Get("pattern")
	idx := -1
	for i, p := range a.resp.Patterns {
		if p.ID == want {
			idx = i
		}
	}
	if idx < 0 {
		writeError(w, http.StatusNotFound, errors.New("no such pattern"))
		return
	}
	writeJSON(w, a.patternDetail(idx))
}

// patternDetail computes per-field metrics for one pattern: fields with at
// most 20 distinct values get a breakdown, mostly-numeric ones get
// median/p95/max per time slot. It feeds both the detail panel and the
// Investigate context, so the agent sees exactly the numbers the page shows.
func (a *analysis) patternDetail(idx int) demoPatternDetail {
	tmpl := fieldTokens(a.resp.Patterns[idx].Template)
	var slots []int
	for i, t := range tmpl {
		if t == "<*>" {
			slots = append(slots, i)
		}
	}
	nb := len(a.resp.Patterns[idx].Buckets)
	values := make([][]string, len(slots))
	stamps := []*time.Time{}
	out := demoPatternDetail{Fields: []demoField{}, Summary: []fieldSummary{}}
	for i, tl := range a.lines {
		if a.owner[i] != idx {
			continue
		}
		if ts := tl.Timestamp; ts != nil {
			if out.FirstSeen == nil || ts.Before(*out.FirstSeen) {
				out.FirstSeen = ts
			}
			if out.LastSeen == nil || ts.After(*out.LastSeen) {
				out.LastSeen = ts
			}
		}
		toks := fieldTokens(tl.DrainLine())
		if len(toks) != len(tmpl) {
			continue
		}
		stamps = append(stamps, tl.Timestamp)
		for k, s := range slots {
			values[k] = append(values[k], toks[s])
		}
	}
	for k, vals := range values {
		if len(vals) == 0 {
			continue
		}
		f := demoField{Index: k + 1}
		counts := map[string]int{}
		for _, v := range vals {
			counts[v]++
		}
		out.Summary = append(out.Summary, summarizeField(k+1, vals, counts))
		if len(counts) <= 20 {
			if len(counts) == 1 {
				continue
			}
			f.Kind = "text"
			for v, c := range counts {
				f.Breakdown = append(f.Breakdown, valueCount{v, c})
			}
			sort.Slice(f.Breakdown, func(i, j int) bool {
				if f.Breakdown[i].Count != f.Breakdown[j].Count {
					return f.Breakdown[i].Count > f.Breakdown[j].Count
				}
				return f.Breakdown[i].Value < f.Breakdown[j].Value
			})
			if len(f.Breakdown) > 8 {
				for _, vc := range f.Breakdown[8:] {
					f.Other += vc.Count
				}
				f.Breakdown = f.Breakdown[:8]
			}
			out.Fields = append(out.Fields, f)
			continue
		}
		nums := make([]float64, 0, len(vals))
		numStamps := make([]*time.Time, 0, len(vals))
		units := map[string]int{}
		for j, v := range vals {
			m := numberValue.FindStringSubmatch(v)
			if m == nil {
				continue
			}
			var x float64
			fmt.Sscan(strings.TrimSuffix(v, m[1]), &x)
			nums = append(nums, x)
			numStamps = append(numStamps, stamps[j])
			units[m[1]]++
		}
		if float64(len(nums)) < 0.9*float64(len(vals)) {
			// Many distinct non-numeric values (ids, hosts): nothing useful to chart.
			continue
		}
		f.Kind = "number"
		// Only show a unit read from the values themselves (123ms, 4.2s); never guess.
		if len(units) == 1 {
			for u := range units {
				f.Unit = u
			}
		}
		f.Overall = summarize(nums)
		if nb > 0 && a.resp.BucketStart != nil {
			per := make([][]float64, nb)
			for j, x := range nums {
				if ts := numStamps[j]; ts != nil {
					b := int(ts.Unix()-a.resp.BucketStart.Unix()) / int(a.resp.BucketSeconds)
					if b >= 0 && b < nb {
						per[b] = append(per[b], x)
					}
				}
			}
			f.Buckets = make([]*fieldStat, nb)
			for b, xs := range per {
				if len(xs) > 0 {
					f.Buckets[b] = summarize(xs)
				}
			}
		}
		out.Fields = append(out.Fields, f)
	}
	return out
}

func summarize(xs []float64) *fieldStat {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	q := func(p float64) float64 { return s[int(math.Ceil(p*float64(len(s))))-1] }
	return &fieldStat{Median: q(0.5), P95: q(0.95), Max: s[len(s)-1]}
}

const gcpImportLimit = 100000

type gcpImportRequest struct {
	Project string    `json:"project"`
	Filter  string    `json:"filter"`
	Since   string    `json:"since"` // e.g. "1h"; ignored when From/To are set
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Service string    `json:"service"`
}

// handleImportGCP pulls a Cloud Logging snapshot into a new workspace and
// finds its patterns, both in the background; poll /api/demo/status.
func (d *demoAPI) handleImportGCP(w http.ResponseWriter, r *http.Request) {
	if d.fetcher == nil {
		writeError(w, http.StatusFailedDependency, errors.New("GCP import is not available"))
		return
	}
	var req gcpImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Project = strings.TrimSpace(req.Project)
	if req.Project == "" {
		writeError(w, http.StatusBadRequest, errors.New("project is required"))
		return
	}
	to, from := req.To, req.From
	if from.IsZero() || to.IsZero() {
		since, err := time.ParseDuration(req.Since)
		if err != nil || since <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("choose a time range"))
			return
		}
		to = time.Now().UTC()
		from = to.Add(-since)
	}
	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, errors.New("the start must be before the end"))
		return
	}
	svc := cleanServiceName(req.Service)
	if strings.TrimSpace(req.Service) == "" {
		svc = cleanServiceName(req.Project)
	}
	id, dir, err := d.newWorkspaceDir(svc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "service"), []byte(svc), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := workspace.RunImport(ctx, workspace.ImportConfig{
			Dir: dir, Provider: "gcp", Project: req.Project, Filter: req.Filter,
			From: from, To: to, Limit: gcpImportLimit, Fetcher: d.fetcher,
		})
		if err != nil {
			slog.Error("gcp import failed", "workspace", id, "error", err)
			return
		}
		if res.EntryCount == 0 {
			return
		}
		if _, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, APIKey: d.namingKey(svc), Model: d.model, Mask: d.namingMask(svc)}); err != nil {
			slog.Error("discovery failed", "workspace", id, "error", err)
		}
	}()
	writeJSON(w, map[string]string{"id": id, "service": svc})
}

// latestImport returns the newest provider import for a workspace, if any.
func latestImport(dir string) *workspace.ImportRunRecord {
	records, err := workspace.ListImportRunRecords(dir)
	if err != nil || len(records) == 0 {
		return nil
	}
	latest := records[0]
	for _, r := range records[1:] {
		if r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	return &latest
}

func describeImport(rec *workspace.ImportRunRecord) string {
	span := rec.To.Sub(rec.From).Round(time.Minute)
	label := strings.TrimSuffix(strings.TrimSuffix(span.String(), "0s"), "0m")
	return fmt.Sprintf("GCP · %s · last %s", rec.Project, label)
}

type gcpRecent struct {
	Projects []string `json:"projects"`
	Filters  []string `json:"filters"`
}

// handleGCPRecent lists past projects and filters for autocomplete.
func (d *demoAPI) handleGCPRecent(w http.ResponseWriter, _ *http.Request) {
	out := gcpRecent{Projects: []string{}, Filters: []string{}}
	entries, _ := os.ReadDir(d.root)
	seenP, seenF := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		records, _ := workspace.ListImportRunRecords(filepath.Join(d.root, e.Name()))
		for _, rec := range records {
			if rec.Project != "" && !seenP[rec.Project] {
				seenP[rec.Project] = true
				out.Projects = append(out.Projects, rec.Project)
			}
			if rec.Filter != "" && !seenF[rec.Filter] {
				seenF[rec.Filter] = true
				out.Filters = append(out.Filters, rec.Filter)
			}
		}
	}
	writeJSON(w, out)
}

func summarizeField(index int, vals []string, counts map[string]int) fieldSummary {
	fs := fieldSummary{Index: index, Lines: len(vals), Distinct: len(counts), Top: []valueCount{}}
	for v, c := range counts {
		fs.Top = append(fs.Top, valueCount{v, c})
	}
	sort.Slice(fs.Top, func(i, j int) bool {
		if fs.Top[i].Count != fs.Top[j].Count {
			return fs.Top[i].Count > fs.Top[j].Count
		}
		return fs.Top[i].Value < fs.Top[j].Value
	})
	if len(fs.Top) > 8 {
		fs.Top = fs.Top[:8]
	}
	nums := make([]float64, 0, len(vals))
	units := map[string]bool{}
	for _, v := range vals {
		if m := numberValue.FindStringSubmatch(v); m != nil {
			var x float64
			fmt.Sscan(strings.TrimSuffix(v, m[1]), &x)
			nums = append(nums, x)
			units[m[1]] = true
		}
	}
	switch {
	case len(counts) == 1:
		fs.Kind = "same"
	case len(counts) <= 20:
		fs.Kind = "values"
	case float64(len(nums)) >= 0.9*float64(len(vals)):
		fs.Kind = "numbers"
		fs.Overall = summarize(nums)
		if len(units) == 1 {
			for u := range units {
				fs.Unit = u
			}
		}
	default:
		fs.Kind = "distinct"
	}
	return fs
}

// Naming sends sample lines to OpenRouter, so for each service it only runs
// after the user has seen the preview and chosen "Send" (or "don't ask again").

func (d *demoAPI) namingFlag(service string) string {
	return filepath.Join(d.rulesDir, service+".naming-approved")
}

func (d *demoAPI) namingApproved(service string) bool {
	_, err := os.Stat(d.namingFlag(service))
	return err == nil
}

// namingMask reports whether sensitive-looking bits are masked before naming
// (the default; turned off only by approving with "Mask before sending" off).
func (d *demoAPI) namingMask(service string) bool {
	b, err := os.ReadFile(d.namingFlag(service))
	if err != nil {
		return true
	}
	var v struct {
		Mask *bool `json:"mask"`
	}
	if json.Unmarshal(b, &v) != nil || v.Mask == nil {
		return true
	}
	return *v.Mask
}

// namingKey returns the OpenRouter key only when naming is approved for the
// service; otherwise discovery keeps raw-text names.
func (d *demoAPI) namingKey(service string) string {
	if d.namingApproved(service) {
		return d.apiKey
	}
	return ""
}

type previewPattern struct {
	Template string   `json:"template"`
	Samples  []string `json:"samples"`
}

func (d *demoAPI) handleLLMPreview(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ws")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, errors.New("invalid workspace"))
		return
	}
	batches, err := workspace.LabelPreview(r.Context(), filepath.Join(d.root, id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := struct {
		Model        string           `json:"model"`
		KeySet       bool             `json:"keySet"`
		Patterns     []previewPattern `json:"patterns"`
		SampleLines  int              `json:"sampleLines"`
		Tokens       int              `json:"tokens"`
		TokensMasked int              `json:"tokensMasked"`
		Mask         bool             `json:"mask"`
		Requests     int              `json:"requests"`
	}{Model: llmconfig.ResolveModel(d.model), KeySet: d.apiKey != "", Patterns: []previewPattern{}, Requests: len(batches),
		Mask: d.namingMask(readService(filepath.Join(d.root, id), id))}
	for _, batch := range batches {
		out.Tokens += len(semantic.BuildPrompt(batch)) / 4
		out.TokensMasked += len(semantic.BuildPrompt(semantic.MaskInputs(batch))) / 4
		for _, p := range batch {
			out.Patterns = append(out.Patterns, previewPattern{Template: p.Pattern, Samples: p.Samples})
			out.SampleLines += len(p.Samples)
		}
	}
	writeJSON(w, out)
}

// handleName names the patterns of a workspace via OpenRouter (re-running
// discovery with the key) and optionally remembers the approval.
func (d *demoAPI) handleName(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("ws")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, errors.New("invalid workspace"))
		return
	}
	if d.apiKey == "" {
		writeError(w, http.StatusPreconditionFailed, errors.New("Set OPENROUTER_API_KEY and restart lapp to name patterns."))
		return
	}
	dir := filepath.Join(d.root, id)
	service := readService(dir, id)
	mask := r.URL.Query().Get("mask") != "0"
	if r.URL.Query().Get("remember") == "1" {
		if err := os.MkdirAll(d.rulesDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		flag, _ := json.Marshal(map[string]any{"approvedAt": time.Now().UTC().Format(time.RFC3339), "mask": mask})
		if err := os.WriteFile(d.namingFlag(service), flag, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	runID, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := workspace.Discover(ctx, workspace.DiscoveryConfig{Dir: dir, RunID: runID.String(), APIKey: d.apiKey, Model: d.model, Mask: mask}); err != nil {
			slog.Error("naming failed", "workspace", id, "error", err)
		}
	}()
	writeJSON(w, map[string]string{"id": id})
}
