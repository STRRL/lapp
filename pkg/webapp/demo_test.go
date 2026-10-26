package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
