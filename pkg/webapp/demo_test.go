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
	if len(c.Gone) != 1 || !strings.Contains(c.Gone[0].Template, "old job done") {
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
