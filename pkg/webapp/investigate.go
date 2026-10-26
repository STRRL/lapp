package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
	"github.com/strrl/lapp/pkg/analyzer"
)

// Investigate runs the user's local agent (Claude Code or Codex over ACP)
// against the filtered patterns of one workspace and returns a structured,
// citation-bearing answer.

type investigation struct {
	ID         string     `json:"id"`
	Workspace  string     `json:"workspace"`
	Agent      string     `json:"agent"`
	Question   string     `json:"question"`
	Pattern    string     `json:"pattern,omitempty"`
	State      string     `json:"state"` // running, succeeded, failed, stopped
	Phase      string     `json:"phase"` // starting, reading, writing
	Answer     string     `json:"answer,omitempty"`
	ErrorCode  string     `json:"errorCode,omitempty"` // not_found, auth, failed
	Error      string     `json:"error,omitempty"`
	Refs       []string   `json:"refs"`                 // P1… → pattern id (template)
	Unverified []string   `json:"unverified,omitempty"` // numbers in the answer not found in context.md
	Previous   string     `json:"previous,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	cancel     context.CancelFunc
}

type investigations struct {
	mu   sync.Mutex
	jobs map[string]*investigation
}

func (j *investigations) get(id string) (investigation, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	inv, ok := j.jobs[id]
	if !ok {
		return investigation{}, false
	}
	return *inv, true
}

func (j *investigations) update(id string, f func(*investigation)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if inv, ok := j.jobs[id]; ok {
		f(inv)
	}
}

// runInvestigation is swappable in tests.
var runInvestigation = func(ctx context.Context, provider, dir, question, system string, onProgress func(string)) (string, error) {
	return analyzer.RunAgentWithPrompt(ctx, analyzer.Config{Provider: provider, OnProgress: onProgress, RawQuestion: true,
		TapePath: filepath.Join(dir, "investigations", "tape.jsonl")}, dir, question, system)
}

var agentBinary = map[string]string{analyzer.ProviderClaude: "claude", analyzer.ProviderCodex: "codex"}

func (d *demoAPI) handleInvestigateStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workspace string `json:"workspace"`
		Agent     string `json:"agent"`
		Question  string `json:"question"`
		Pattern   string `json:"pattern"`
		FollowUp  string `json:"followUp"` // previous investigation id
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeError(w, http.StatusBadRequest, errors.New("ask a question first"))
		return
	}
	if req.Agent != analyzer.ProviderCodex {
		req.Agent = analyzer.ProviderClaude
	}
	a, err := d.analyze(r.Context(), req.Workspace)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("Wait for patterns to finish, then investigate."))
		return
	}
	var filtered []string
	if raw := r.URL.Query()["filtered"]; raw != nil {
		filtered = raw
	}
	dir := filepath.Join(d.root, req.Workspace)
	refs, err := writeInvestigationContext(dir, a, filtered)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	inv := &investigation{
		ID: uuid.NewString(), Workspace: req.Workspace, Agent: req.Agent, Question: req.Question,
		Pattern: req.Pattern, State: "running", Phase: "starting", Refs: refs, StartedAt: time.Now().UTC(),
	}
	question := buildInvestigationQuestion(req.Question, req.Pattern, refs)
	if prev, ok := d.jobs.get(req.FollowUp); ok && prev.Answer != "" {
		inv.Previous = prev.ID
		question = fmt.Sprintf("Earlier question: %s\n\nYour earlier answer:\n%s\n\nFollow-up question: %s", prev.Question, prev.Answer, question)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	inv.cancel = cancel
	d.jobs.mu.Lock()
	d.jobs.jobs[inv.ID] = inv
	d.jobs.mu.Unlock()

	if _, err := exec.LookPath(agentBinary[req.Agent]); err != nil {
		cancel()
		d.jobs.update(inv.ID, func(j *investigation) {
			j.State, j.ErrorCode, j.Error = "failed", "not_found", err.Error()
		})
		writeJSON(w, map[string]string{"id": inv.ID})
		return
	}

	go func() {
		defer cancel()
		answer, err := runInvestigation(ctx, req.Agent, dir, question, investigationSystemPrompt(dir), func(phase string) {
			d.jobs.update(inv.ID, func(j *investigation) { j.Phase = phase })
		})
		d.jobs.update(inv.ID, func(j *investigation) {
			now := time.Now().UTC()
			j.FinishedAt = &now
			switch {
			case j.State == "stopped":
			case err != nil:
				j.State, j.ErrorCode, j.Error = "failed", classifyAgentError(err), err.Error()
			default:
				j.State, j.Answer = "succeeded", trimToAnswer(answer)
				ctxText, _ := os.ReadFile(filepath.Join(dir, "investigations", "context.md"))
				j.Unverified = unverifiedNumbers(j.Answer, string(ctxText))
			}
		})
		if err != nil {
			slog.Warn("investigation failed", "workspace", req.Workspace, "agent", req.Agent, "error", err)
		}
		if done, ok := d.jobs.get(inv.ID); ok {
			if data, err := json.MarshalIndent(done, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "investigations", done.ID+".json"), data, 0o644)
			}
		}
	}()
	writeJSON(w, map[string]string{"id": inv.ID})
}

func (d *demoAPI) handleInvestigateGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	inv, ok := d.jobs.get(id)
	if !ok {
		inv, ok = d.loadInvestigation(id)
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such investigation"))
		return
	}
	writeJSON(w, inv)
}

func (d *demoAPI) handleInvestigateStop(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	d.jobs.update(id, func(j *investigation) {
		if j.State == "running" {
			j.State = "stopped"
			if j.cancel != nil {
				j.cancel()
			}
		}
	})
	inv, _ := d.jobs.get(id)
	writeJSON(w, inv)
}

var authHints = regexp.MustCompile(`(?i)(auth|login|log in|sign in|unauthori[sz]ed|401|api key|credential)`)

func classifyAgentError(err error) string {
	if authHints.MatchString(err.Error()) {
		return "auth"
	}
	return "failed"
}

// writeInvestigationContext writes investigations/context.md: every pattern
// with an id (P1…), counts, timing and samples, so the agent can cite them.
func writeInvestigationContext(dir string, a *analysis, filteredOverride []string) ([]string, error) {
	var b strings.Builder
	resp := a.resp
	total := resp.Workspace.LineCount
	fmt.Fprintf(&b, "# lapp investigation context\n\nService: %s\nLogs: %s\nLines: %d\n", resp.Workspace.Service, resp.Workspace.Title, total)
	if resp.From != nil && resp.To != nil {
		fmt.Fprintf(&b, "Time range (UTC): %s – %s\nTime slots: %s each\n", fmtTime(*resp.From), fmtTime(*resp.To), bucketUnit(resp.BucketSeconds))
	}
	b.WriteString("\nPatterns are listed by line count. Cite them as [P<n>] or [P<n> · <time range>] using the times shown here.\n")
	b.WriteString("All numbers below are exactly what the lapp page shows. Use only these numbers in your answer.\n")
	b.WriteString("Patterns marked FILTERED were judged as noise by the user; mention them only if they matter.\n\n")
	refs := make([]string, len(resp.Patterns))
	for i, p := range resp.Patterns {
		refs[i] = p.ID
		filtered := p.Filtered
		if filteredOverride != nil {
			filtered = contains(filteredOverride, p.ID)
		}
		fmt.Fprintf(&b, "## P%d %s\n", i+1, p.Template)
		fmt.Fprintf(&b, "- display name: %s\n", p.Name)
		facts := []string{fmt.Sprintf("%d lines (%s)", p.Count, formatPercent(float64(p.Count)/float64(max(total, 1))))}
		if p.Level != "" {
			facts = append(facts, "level "+p.Level)
		}
		if filtered {
			facts = append(facts, "FILTERED as noise")
		}
		if p.SpikeAt != nil && resp.BucketStart != nil {
			facts = append(facts, fmt.Sprintf("spike: %d lines in %s", p.Buckets[*p.SpikeAt], slotRange(resp, *p.SpikeAt)))
		}
		if p.NewAt != nil && resp.BucketStart != nil {
			facts = append(facts, "first seen "+slotRange(resp, *p.NewAt))
		}
		b.WriteString("- " + strings.Join(facts, "; ") + "\n")
		if resp.BucketStart != nil {
			b.WriteString("- lines per slot (slots not listed had 0): " + slotCounts(resp, p.Buckets) + "\n")
		}
		for _, f := range a.patternDetail(i).Fields {
			b.WriteString("- " + describeField(f) + "\n")
		}
		b.WriteString("- samples:\n")
		for _, line := range p.Samples[:min(3, len(p.Samples))] {
			b.WriteString("    " + truncate(line, 300) + "\n")
		}
		b.WriteString("\n")
	}
	if resp.Unmatched.Count > 0 {
		fmt.Fprintf(&b, "## Unmatched lines (%d, fit no pattern)\n", resp.Unmatched.Count)
		for _, line := range resp.Unmatched.Samples[:min(20, len(resp.Unmatched.Samples))] {
			b.WriteString("    " + truncate(line, 300) + "\n")
		}
	}
	out := filepath.Join(dir, "investigations")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	return refs, os.WriteFile(filepath.Join(out, "context.md"), []byte(b.String()), 0o644)
}

func fmtTime(t time.Time) string { return t.UTC().Format("Jan 2 15:04") }

func slotRange(resp demoPatternsResponse, i int) string {
	start := resp.BucketStart.Add(time.Duration(int64(i)*resp.BucketSeconds) * time.Second)
	end := start.Add(time.Duration(resp.BucketSeconds) * time.Second)
	if resp.BucketSeconds >= 86400 {
		return start.Format("Jan 2")
	}
	return start.Format("Jan 2 15:04") + "–" + end.Format("15:04")
}

func slotCounts(resp demoPatternsResponse, buckets []int) string {
	var parts []string
	for i, c := range buckets {
		if c > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", slotRange(resp, i), c))
		}
	}
	if len(parts) > 60 {
		parts = append(parts[:60], "…")
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func buildInvestigationQuestion(question, pattern string, refs []string) string {
	if pattern == "" {
		return question
	}
	for i, id := range refs {
		if id == pattern {
			return fmt.Sprintf("%s\n\n(This question is about [P%d].)", question, i+1)
		}
	}
	return question
}

func investigationSystemPrompt(dir string) string {
	return fmt.Sprintf(`You are helping a developer find the problem in their logs, using lapp's pattern analysis.

Stay inside %[1]s. Do not modify any file.
Read %[1]s/investigations/context.md first. It lists every log pattern as P1, P2, … with line counts, lines per time slot, field value breakdowns and sample lines — exactly the numbers the user sees on the lapp page.
Do not open the raw logs or run commands: context.md has everything the user can see, and every number in your answer must be one that appears in it.

Answer in Markdown with exactly these sections, in this order:

# <one line naming the problem>
## Likely cause
At most 3 short sentences, plain language. It is a hypothesis; say so if the evidence is thin.
## Timeline
- <time> — <what happened> [P<n>]
## Evidence
At most 5 bullets, one sentence each.
- <fact> [P<n> · <time range>]
## What to check next
At most 4 bullets, one sentence each.
- <concrete check or fix>

Rules:
- Every Timeline and Evidence bullet cites at least one pattern as [P<n>] or [P<n> · <time range>], using ids and times from context.md. Leave out the time range when the fact covers the whole log.
- In prose, refer to a pattern by its display name or a few words of its text, never by a bare id like P3; ids only appear inside the [ ] citations.
- Every number you write must be copied verbatim from context.md. Do not compute new numbers (no ratios like "6×", no differences, no ranges you worked out). If a number you want is not there, describe it in words ("several times higher").
- No preamble before the first heading.`, dir)
}

// handleAgents reports which local agents can run an investigation.
func (d *demoAPI) handleAgents(w http.ResponseWriter, _ *http.Request) {
	out := map[string]bool{}
	for provider, bin := range agentBinary {
		_, err := exec.LookPath(bin)
		out[provider] = err == nil
	}
	writeJSON(w, out)
}

// loadInvestigation finds a finished investigation saved in any workspace.
func (d *demoAPI) loadInvestigation(id string) (investigation, bool) {
	if _, err := uuid.Parse(id); err != nil {
		return investigation{}, false
	}
	matches, _ := filepath.Glob(filepath.Join(d.root, "*", "investigations", id+".json"))
	for _, m := range matches {
		var inv investigation
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &inv) == nil {
			return inv, true
		}
	}
	return investigation{}, false
}

// describeField renders one field's metrics as the page shows them.
func describeField(f demoField) string {
	if f.Kind == "text" {
		total := f.Other
		for _, v := range f.Breakdown {
			total += v.Count
		}
		parts := make([]string, 0, len(f.Breakdown)+1)
		for _, v := range f.Breakdown {
			parts = append(parts, fmt.Sprintf("%q=%d", v.Value, v.Count))
		}
		if f.Other > 0 {
			parts = append(parts, fmt.Sprintf("other=%d", f.Other))
		}
		return fmt.Sprintf("field %d (<*> #%d) values, %d distinct shown of %d lines: %s", f.Index, f.Index, len(f.Breakdown), total, strings.Join(parts, ", "))
	}
	o := f.Overall
	return fmt.Sprintf("field %d (<*> #%d) numbers%s: median %g, p95 %g, max %g", f.Index, f.Index, unitSuffix(f.Unit), o.Median, o.P95, o.Max)
}

func unitSuffix(u string) string {
	if u == "" {
		return ""
	}
	return " in " + u
}

// trimToAnswer drops anything the agent said before the first "# " heading
// (thinking out loud between tool calls).
func trimToAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	for i := 0; i < len(answer); {
		j := strings.IndexByte(answer[i:], '#')
		if j < 0 {
			break
		}
		k := i + j
		if (k == 0 || answer[k-1] == '\n' || answer[k-1] == '.' || answer[k-1] == ' ') && strings.HasPrefix(answer[k:], "# ") {
			return strings.TrimSpace(answer[k:])
		}
		i = k + 1
	}
	return answer
}

var (
	citationRe = regexp.MustCompile(`\[P\d+[^\]]*\]`)
	clockRe    = regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
	dateRe     = regexp.MustCompile(`\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{1,2}\b`)
	numberRe   = regexp.MustCompile(`\d+(?:\.\d+)?`)
	ratioRe    = regexp.MustCompile(`\d+(?:\.\d+)?\s?[×x]\b|\d+(?:\.\d+)?×`)
)

// unverifiedNumbers lists numbers in the answer (outside citations, dates
// and clock times) that do not appear anywhere in the context the agent was
// given, so the UI can flag them instead of presenting them as fact.
func unverifiedNumbers(answer, context string) []string {
	known := map[string]bool{}
	for _, n := range numberRe.FindAllString(context, -1) {
		known[n] = true
	}
	text := citationRe.ReplaceAllString(answer, " ")
	text = dateRe.ReplaceAllString(text, " ")
	text = clockRe.ReplaceAllString(text, " ")
	var out []string
	seen := map[string]bool{}
	// Ratios are computed by the agent, never shown on the page.
	for _, r := range ratioRe.FindAllString(text, -1) {
		if !seen[r] && !strings.Contains(context, r) {
			seen[r] = true
			out = append(out, r)
		}
	}
	text = ratioRe.ReplaceAllString(text, " ")
	for _, n := range numberRe.FindAllString(text, -1) {
		if known[n] || seen[n] || len(n) == 1 {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
