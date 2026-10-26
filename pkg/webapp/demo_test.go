package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/strrl/lapp/pkg/victorialogs"
	"github.com/strrl/lapp/pkg/workspace"
)

func TestDemoGCPImportFindsPatterns(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	d.fetcher = func(_ context.Context, req workspace.ImportRequest, write workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		base := req.From
		for i := 0; i < 50; i++ {
			line := fmt.Sprintf(`{"ts":%q,"severity":"INFO","payload":{"message":"health check ok from 10.0.0.%d"}}`, base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i%5)
			if err := write(line); err != nil {
				return workspace.ImportFetchResult{}, err
			}
		}
		return workspace.ImportFetchResult{}, nil
	}
	mux := http.NewServeMux()
	d.register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/demo/import-gcp", strings.NewReader(`{"project":"my-proj","since":"1h","service":"checkout"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var started struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &started)

	var st demoStatus
	for i := 0; i < 100; i++ {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/status?ws="+started.ID, nil))
		_ = json.Unmarshal(rec.Body.Bytes(), &st)
		if st.State == "succeeded" || st.State == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.State != "succeeded" || st.Source != "gcp" {
		t.Fatalf("status = %+v", st)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/patterns?ws="+started.ID, nil))
	var resp demoPatternsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Workspace.Service != "checkout" || resp.Workspace.Title != "GCP · my-proj · last 1h" {
		t.Fatalf("workspace = %+v", resp.Workspace)
	}
	if len(resp.Patterns) != 1 || resp.Patterns[0].Count != 50 {
		t.Fatalf("patterns = %+v", resp.Patterns)
	}
}

func TestDemoCompareWithPreviousImport(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	day := func(n int, lines map[string]int) string {
		var b strings.Builder
		base := time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC)
		i := 0
		for msg, count := range lines {
			for j := 0; j < count; j++ {
				fmt.Fprintf(&b, "%s INFO %s %d\n", base.Add(time.Duration(i)*time.Second).Format(time.RFC3339), msg, j)
				i++
			}
		}
		return b.String()
	}
	ctx := context.Background()
	first, err := d.importLog(ctx, "api-1.log", strings.NewReader(day(1, map[string]int{"request served in": 50, "cache warmed for": 20, "old job done id": 10})))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := d.importLog(ctx, "api-2.log", strings.NewReader(day(2, map[string]int{"request served in": 30, "cache warmed for": 40, "payment failed for": 10})))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.analyze(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	c := a.resp.Compare
	if c == nil || c.PreviousID != first {
		t.Fatalf("compare = %+v, want previous %s", c, first)
	}
	got := map[string]string{}
	for _, p := range a.resp.Patterns {
		got[strings.Fields(p.Template)[1]] = p.SinceLast
	}
	if got["payment"] != "new" || got["cache"] != "up" {
		t.Fatalf("sinceLast = %v", got)
	}
	if c.New != 1 || c.Changed != 1 || len(c.Gone) != 1 || !strings.Contains(c.Gone[0].Template, "old job done") {
		t.Fatalf("gone = %+v", c.Gone)
	}
}

func TestInvestigateAnswerChecks(t *testing.T) {
	raw := "Let me look at the logs.Now I have enough.# Spike at 06:00\n\n## Likely cause\nErrors rose to 90 lines, a 6× jump; PIDs 32347–32582. [P3 · Dec 4 06:00–07:00]"
	ans := trimToAnswer(raw)
	if !strings.HasPrefix(ans, "# Spike at 06:00") {
		t.Fatalf("trim = %q", ans)
	}
	got := unverifiedNumbers(ans, "P3 lines per slot: Dec 4 06:00–07:00=90")
	if strings.Join(got, ",") != "6×,32347,32582" {
		t.Fatalf("unverified = %v", got)
	}
}

func TestSummarizeLog(t *testing.T) {
	var b strings.Builder
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "%s INFO health check ok from 10.0.0.%d\n", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i%7)
	}
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "%s ERROR payment failed for order %d\n", base.Add(5*time.Hour+time.Duration(i)*time.Second).Format(time.RFC3339), i)
	}
	s, err := SummarizeLog(context.Background(), filepath.Join(t.TempDir(), "workspaces"), "", "checkout.log", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if s.Service != "checkout" || s.Lines != 640 || len(s.Patterns) != 2 {
		t.Fatalf("summary = %+v", s)
	}
	if len(s.StartHere) == 0 || !strings.HasPrefix(s.StartHere[0].Reason, "Error") {
		t.Fatalf("startHere = %+v", s.StartHere)
	}
	if s.Patterns[0].Filtered || s.Patterns[0].Noise == nil || s.Patterns[0].Noise.Suggestion != "likely" {
		t.Fatalf("health checks should be marked likely noise but not hidden: %+v", s.Patterns[0])
	}

	// Shaped like a plain-text Postgres export (no level field): two steady,
	// high-share error messages are an incident, never noise; health checks are.
	b.Reset()
	for m := 0; m < 60; m++ {
		at := base.Add(time.Duration(m) * time.Minute)
		for i := 0; i < 15; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main column \"member_created_here\" does not exist at character %d\n", at.Add(time.Duration(i)*time.Second).Format(time.RFC3339), 4000+i, 40+i)
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main invalid input syntax for type uuid: \"x%d\"\n", at.Add(time.Duration(20+i)*time.Second).Format(time.RFC3339), 5000+i, i)
		}
		for i := 0; i < 4; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main health check ok from 10.0.0.%d\n", at.Add(time.Duration(30+i)*time.Second).Format(time.RFC3339), 6000+i, i)
		}
	}
	s, err = SummarizeLog(context.Background(), filepath.Join(t.TempDir(), "workspaces"), "", "pg.log", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	marks := map[string]string{}
	for _, p := range s.Patterns {
		marks[p.Name] = ""
		if p.Noise != nil {
			marks[p.Name] = p.Noise.Suggestion
		}
	}
	starts := map[string]bool{}
	for _, f := range s.StartHere {
		starts[f.Name] = true
	}
	for name, mark := range marks {
		isErr := strings.Contains(name, "does not exist") || strings.Contains(name, "invalid input")
		switch {
		case isErr && (mark != "" || !starts[name]):
			t.Errorf("%q: noise mark %q, in Start here %v; want no mark and in Start here", name, mark, starts[name])
		case strings.Contains(name, "health check") && mark != "likely":
			t.Errorf("%q: noise mark %q, want likely", name, mark)
		}
	}
	if len(marks) != 3 {
		t.Fatalf("patterns = %v", marks)
	}
	var text strings.Builder
	s.WriteText(&text)
	if !strings.Contains(text.String(), "\nNothing hidden · 3 patterns · ") {
		t.Errorf("summary text should say nothing is hidden:\n%s", text.String())
	}
	for _, line := range strings.Split(text.String(), "\n") {
		if strings.Contains(line, "does not exist") && strings.HasPrefix(line, " ?") {
			t.Errorf("summary text marks an error as likely noise: %q", line)
		}
	}
}

func TestDemoNoiseDecisionsPersistPerService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	var b strings.Builder
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "%s INFO health check ok from 10.0.0.%d\n", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i%7)
		fmt.Fprintf(&b, "%s INFO cache refreshed in %d ms\n", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i%9)
	}
	ctx := context.Background()
	mux := http.NewServeMux()
	d.register(mux)
	decide := func(ws, decision string, ids ...string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"patterns": ids, "decision": decision})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/demo/rules?ws="+ws, strings.NewReader(string(body))))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", decision, rec.Code, rec.Body)
		}
	}
	byWord := func(ws string) map[string]demoPattern {
		t.Helper()
		a, err := d.analyze(ctx, ws)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]demoPattern{}
		for _, p := range a.resp.Patterns {
			out[strings.Fields(p.Template)[1]] = p
		}
		return out
	}

	first, err := d.importLog(ctx, "api-1.log", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	got := byWord(first)
	if got["health"].Suggestion != "noise" || got["health"].Filtered || got["cache"].Suggestion != "noise" {
		t.Fatalf("suggestions should only mark, not hide: %+v", got)
	}
	decide(first, "hide", got["health"].ID)
	decide(first, "not-noise", got["cache"].ID)

	// A later import of the same service applies both decisions.
	second, err := d.importLog(ctx, "api-2.log", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	got = byWord(second)
	if !got["health"].Filtered || got["cache"].Filtered || got["cache"].Suggestion != "" {
		t.Fatalf("decisions not applied: %+v", got)
	}
	decide(second, "unhide", got["health"].ID)
	if got = byWord(second); got["health"].Filtered || got["health"].Suggestion != "noise" {
		t.Fatalf("unhide: %+v", got["health"])
	}
	// Two Hide clicks sent at the same time must both be kept.
	var wg sync.WaitGroup
	for _, word := range []string{"health", "cache"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			decide(second, "hide", id)
		}(got[word].ID)
	}
	wg.Wait()
	if got = byWord(second); !got["health"].Filtered || !got["cache"].Filtered {
		t.Fatalf("concurrent hides lost a decision: health=%v cache=%v", got["health"].Filtered, got["cache"].Filtered)
	}
}

func TestDemoVictoriaLogsImportSplitsServices(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	d.victoriaLogsStreams = func(_ context.Context, req victorialogs.FetchRequest) ([]victorialogs.Stream, error) {
		if req.URL != "http://logs.example.com:9428" || req.Query != "error" {
			t.Errorf("streams request = %+v", req)
		}
		return []victorialogs.Stream{
			{ID: "b2", Labels: `{app="api",env="staging"}`, Hits: 10},
			{ID: "a1", Labels: `{app="api",env="prod"}`, Hits: 20},
			{ID: "c3", Labels: `{env="prod",service.name="Checkout API"}`, Hits: 30},
		}, nil
	}
	d.fetcher = func(_ context.Context, req workspace.ImportRequest, write workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		if req.Provider != victorialogs.Provider {
			t.Errorf("provider = %q", req.Provider)
		}
		for i := 0; i < 20; i++ {
			line := fmt.Sprintf(`{"_msg":"error talking to db 10.0.0.%d","_time":%q}`, i%5, req.From.Add(time.Duration(i)*time.Minute).Format(time.RFC3339))
			if err := write(line); err != nil {
				return workspace.ImportFetchResult{}, err
			}
		}
		return workspace.ImportFetchResult{}, nil
	}
	mux := http.NewServeMux()
	d.register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/demo/import-victorialogs", strings.NewReader(`{"url":"http://logs.example.com:9428","query":"error","since":"1h"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var started struct{ Workspaces []victoriaLogsStarted }
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	want := []victoriaLogsStarted{{ID: "api", Service: "api"}, {ID: "checkout-api", Service: "checkout-api"}}
	if fmt.Sprint(started.Workspaces) != fmt.Sprint(want) {
		t.Fatalf("workspaces = %+v, want %+v", started.Workspaces, want)
	}

	for _, ws := range started.Workspaces {
		var st demoStatus
		for i := 0; i < 100; i++ {
			rec = httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/status?ws="+ws.ID, nil))
			_ = json.Unmarshal(rec.Body.Bytes(), &st)
			if st.State == "succeeded" || st.State == "failed" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if st.State != "succeeded" || st.Source != victorialogs.Provider {
			t.Fatalf("%s status = %+v", ws.ID, st)
		}
	}
	record := latestImport(filepath.Join(root, "api"))
	if record == nil || record.Filter != "_stream_id:in(a1,b2) (error)" || record.URL != "http://logs.example.com:9428" {
		t.Fatalf("api record = %+v", record)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/patterns?ws=api", nil))
	var resp demoPatternsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Workspace.Service != "api" || resp.Workspace.Title != "VictoriaLogs · logs.example.com:9428 · last 1h" {
		t.Fatalf("workspace = %+v", resp.Workspace)
	}
}

func TestPlanVictoriaLogsServicesFallsBackToStreamHash(t *testing.T) {
	plan := planVictoriaLogsServices([]victorialogs.Stream{{ID: "x", Labels: `{env="prod"}`, Hits: 1}}, "*")
	if len(plan) != 1 || !strings.HasPrefix(plan[0].Service, "stream-") || len(plan[0].Service) != len("stream-")+8 || plan[0].Query != "_stream_id:in(x)" {
		t.Fatalf("plan = %+v", plan)
	}
}

// pgPlainLog is shaped like a plain-text Postgres export with no level field:
// two steady error messages, a statement timeout (an error by its words) with
// a spike, and three rare multi-line execution plans that appear late. Extra
// lines are appended as "<minute> <message>" pairs.
func pgPlainLog(day int, extra map[int]string) string {
	var b strings.Builder
	base := time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC)
	at := func(m, s int) string {
		return base.Add(time.Duration(m)*time.Minute + time.Duration(s)*time.Second).Format(time.RFC3339)
	}
	plans := map[int]string{
		45: "duration: 12745.859 ms  plan:\n\tQuery Text: SELECT sr.application_id FROM submissions sr\n\tSeq Scan on submissions sr  (cost=0.00..431.00 rows=10000 width=16)",
		47: "duration: 17384.464 ms  plan:\n\tQuery Text: WITH pgrst_source AS ( SELECT * FROM members WHERE id = $1 )\n\tIndex Scan using members_pkey on members  (cost=0.29..8.30 rows=1 width=64)\n\t  Index Cond: (id = $1)",
		50: "duration: 32898.026 ms  plan:\n\tQuery Text: WITH segment_creators AS ( SELECT creator_id, count(*) FROM segments GROUP BY creator_id )\n\tHashAggregate  (cost=20.00..22.00 rows=200 width=12)\n\t  Group Key: creator_id\n\t  ->  Seq Scan on segments  (cost=0.00..15.00 rows=1000 width=4)",
	}
	for m := 0; m < 60; m++ {
		for i := 0; i < 15; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main column \"member_created_here\" does not exist at character %d\n", at(m, i), 4000+i, 40+i)
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main invalid input syntax for type uuid: \"x%d\"\n", at(m, 20+i), 5000+i, i)
		}
		timeouts := 0
		switch {
		case m == 40:
			timeouts = 12
		case m%5 == 0 && m > 0 && m <= 50:
			timeouts = 1
		}
		for i := 0; i < timeouts; i++ {
			fmt.Fprintf(&b, "%s [%d] app@main canceling statement due to statement timeout\n", at(m, 30+i), 6000+i)
		}
		for start, plan := range plans {
			if m == start || m == start+8 {
				fmt.Fprintf(&b, "%s [%d] app@main LOG:  %s\n", at(m, 50), 7000+m, plan)
			}
		}
		if msg, ok := extra[m]; ok {
			fmt.Fprintf(&b, "%s [%d] app@main %s\n", at(m, 55), 8000+m, msg)
		}
	}
	return b.String()
}

func TestStartHerePlainTextErrors(t *testing.T) {
	names := func(s *Summary) []string {
		var out []string
		for _, f := range s.StartHere {
			out = append(out, f.Name+" | "+f.Reason)
		}
		return out
	}
	s, err := SummarizeLog(context.Background(), filepath.Join(t.TempDir(), "workspaces"), "", "pg.log", strings.NewReader(pgPlainLog(1, nil)))
	if err != nil {
		t.Fatal(err)
	}
	plans := 0
	for _, p := range s.Patterns {
		if strings.Contains(p.Template, "plan:") {
			plans++
		}
	}
	if plans != 3 {
		t.Fatalf("want 3 plan patterns, got %d: %+v", plans, s.Patterns)
	}
	got := names(s)
	want := []string{
		`column "member_created_here" does not exist at character <*> | Steady error · `,
		`invalid input syntax for type uuid: <*> | Steady error · `,
		`canceling statement due to statement timeout | Error · spiked to 12 per minute at Oct 1 00:40`,
	}
	if len(got) != len(want) {
		t.Fatalf("first import: start here = %q, want only the two steady errors and the timeout", got)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Fatalf("first import: start here[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// A second import of the same service: a new non-error pattern is listed
	// after the errors only from 3 lines.
	for _, n := range []int{2, 3} {
		root := filepath.Join(t.TempDir(), "workspaces")
		d := newDemoAPI(root, "", "")
		ctx := context.Background()
		if _, err := d.importLog(ctx, "pg-1.log", strings.NewReader(pgPlainLog(1, nil))); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		extra := map[int]string{}
		for i := 0; i < n; i++ {
			extra[10+i*7] = fmt.Sprintf("checkpoint starting: time %d", i)
		}
		second, err := d.importLog(ctx, "pg-2.log", strings.NewReader(pgPlainLog(2, extra)))
		if err != nil {
			t.Fatal(err)
		}
		a, err := d.analyze(ctx, second)
		if err != nil {
			t.Fatal(err)
		}
		got := names(buildSummary(a))
		listed := len(got) == 4 && strings.Contains(got[3], "checkpoint starting: time <*> | New since last time")
		if n == 2 && len(got) != 3 || n == 3 && !listed {
			t.Fatalf("second import with %d new lines: start here = %q", n, got)
		}
	}
}

func TestIsErrorLike(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{`column "x" does not exist`, true},
		{"canceling statement due to statement timeout", true},
		{"ERROR: invalid input syntax for type uuid", true},
		{"java.io.IOException: broken pipe", true},
		{"TypeError: cannot read properties of undefined", true},
		{"statement: CREATE MATERIALIZED VIEW app_index AS SELECT has_error, is_exception FROM t -- this invalidates the cache", false},
		{"health check ok in 3 ms", false},
		{"statement: CREATE MATERIALIZED VIEW app_index AS\n-- A real edge exists but cannot collapse one-to-one.\nSELECT 1", false},
		{"ERROR: deadlock detected\nDETAIL: Process 1 waits for ShareLock", true},
	}
	for _, tt := range tests {
		if got := isErrorLike(tt.text); got != tt.want {
			t.Errorf("isErrorLike(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestDetectLevelUsesFirstLineOnly(t *testing.T) {
	if got := detectLevel("statement: SELECT 1\n-- ERROR handling is elsewhere"); got != "" {
		t.Fatalf("detectLevel = %q, want no level from a later line", got)
	}
	if got := detectLevel("ERROR: deadlock detected\nDETAIL: waits for ShareLock"); got != "error" {
		t.Fatalf("detectLevel = %q, want error from the first line", got)
	}
}

// The same log exported as text and as NDJSON with a level field mines to
// the same templates; the NDJSON level only sets the pattern's level.
func TestNDJSONLevelStaysOutOfTemplate(t *testing.T) {
	var text, ndjson strings.Builder
	for i := 0; i < 20; i++ {
		ts := time.Date(2026, 10, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)
		msg := fmt.Sprintf(`column "member_created_here" does not exist at character %d`, 40+i%3)
		if i%2 == 1 {
			msg = fmt.Sprintf("connection received: host=10.0.0.%d", i)
		}
		level := map[bool]string{true: "error", false: "info"}[i%2 == 0]
		fmt.Fprintf(&text, "%s %s\n", ts, msg)
		line, _ := json.Marshal(map[string]string{"timestamp": ts, "level": level, "message": msg})
		fmt.Fprintf(&ndjson, "%s\n", line)
	}
	summarize := func(name, body string) map[string]string {
		s, err := SummarizeLog(context.Background(), filepath.Join(t.TempDir(), "workspaces"), "", name, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		levels := map[string]string{}
		for _, p := range s.Patterns {
			levels[p.Template] = p.Level
		}
		return levels
	}
	fromText, fromNDJSON := summarize("pg.log", text.String()), summarize("pg.ndjson", ndjson.String())
	if len(fromText) != 2 || len(fromNDJSON) != 2 {
		t.Fatalf("text patterns = %v, ndjson patterns = %v", fromText, fromNDJSON)
	}
	for template := range fromText {
		level, ok := fromNDJSON[template]
		if !ok {
			t.Fatalf("template %q from text is missing from ndjson %v", template, fromNDJSON)
		}
		want := "info"
		if strings.Contains(template, "does not exist") {
			want = "error"
		}
		if level != want {
			t.Fatalf("ndjson level of %q = %q, want %q", template, level, want)
		}
	}
}

// An import made with an older lapp stays listed, marked, and its patterns
// answer 410 with the service to import again into.
func TestOlderImportIsListedButNotOpened(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	id, err := d.importLog(context.Background(), "pg.log", strings.NewReader(pgPlainLog(1, nil)))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(d.root, id)
	records, err := workspace.ListDiscoveryRunRecords(dir)
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	path := workspace.DiscoveryRunRecordPath(dir, records[0].ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "format")
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	d.register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/workspaces", nil))
	var list []demoWorkspace
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || !list[0].OlderVersion || list[0].ID != id || list[0].Service == "" {
		t.Fatalf("workspaces = %+v", list)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/patterns?ws="+id, nil))
	var body struct {
		OlderVersion bool          `json:"olderVersion"`
		Workspace    demoWorkspace `json:"workspace"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusGone || !body.OlderVersion || body.Workspace.Service != list[0].Service || body.Workspace.Title != list[0].Title {
		t.Fatalf("patterns: %d %s", rec.Code, rec.Body)
	}
}

// UUIDs are variables: lines that differ only in a UUID, or in how many
// UUIDs a list has, are one pattern, and no UUID shows in its template.
func TestUUIDListsAreOnePattern(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 12; i++ {
		ids := make([]string, 1+i%4)
		for k := range ids {
			ids[k] = fmt.Sprintf("'%08x-0b7d-4e21-9a3c-%012x'", i*7+k, i*13+k)
		}
		line, _ := json.Marshal(map[string]string{"level": "info", "message": "select * from items where id in (" + strings.Join(ids, ", ") + ")"})
		fmt.Fprintf(&body, "%s\n", line)
	}
	s, err := SummarizeLog(context.Background(), filepath.Join(t.TempDir(), "workspaces"), "", "items.ndjson", strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Patterns) != 1 || s.Patterns[0].Lines != 12 || strings.Contains(s.Patterns[0].Template, "0b7d") {
		t.Fatalf("patterns = %+v", s.Patterns)
	}
}

// A masked UUID is still a field with its real values: bare, inside a token,
// or after a <*> the log itself contains.
func TestMaskedUUIDKeepsFieldValues(t *testing.T) {
	for _, format := range []string{"user %s logged in", "user (%s) logged in", "rule <*> matched user %s"} {
		t.Run(format, func(t *testing.T) {
			var body strings.Builder
			for i := 0; i < 12; i++ {
				id := fmt.Sprintf("%08x-0b7d-4e21-9a3c-%012x", i, i)
				line, _ := json.Marshal(map[string]string{"level": "info", "message": fmt.Sprintf(format, id)})
				fmt.Fprintf(&body, "%s\n", line)
			}
			d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
			ctx := context.Background()
			id, err := d.importLog(ctx, "users.ndjson", strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			a, err := d.analyze(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.resp.Patterns) != 1 {
				t.Fatalf("patterns = %+v", a.resp.Patterns)
			}
			var uuidField *fieldSummary
			for k, f := range a.patternDetail(0).Summary {
				if f.Distinct == 12 {
					uuidField = &a.patternDetail(0).Summary[k]
				}
			}
			if uuidField == nil || !strings.Contains(uuidField.Top[0].Value, "-0b7d-") {
				t.Fatalf("template %q: summary = %+v", a.resp.Patterns[0].Template, a.patternDetail(0).Summary)
			}
		})
	}
}
