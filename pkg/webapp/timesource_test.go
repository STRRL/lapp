package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTimeColumnFallback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	mux := http.NewServeMux()
	d.register(mux)
	base := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString("level,event,when\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "info,request served %d,%s\n", i, base.Add(time.Duration(i)*time.Hour).Format("2006-01-02 15:04:05"))
	}
	ctx := context.Background()
	id, err := d.importLog(ctx, "orders.csv", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.analyze(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.resp.Time; got.Format != timeFormatCSV || !slices.Equal(got.Columns, []string{"level", "event", "when"}) || a.resp.BucketSeconds != 0 {
		t.Fatalf("before choosing: time = %+v, bucket = %d", got, a.resp.BucketSeconds)
	}

	choose := func(column string) demoPatternsResponse { return chooseTimeColumn(t, mux, id, column) }
	if resp := choose("level"); !resp.Time.ColumnUnreadable || resp.BucketSeconds != 0 || resp.Time.Column != "level" {
		t.Fatalf("unreadable column: time = %+v", resp.Time)
	}
	if resp := choose("when"); resp.Time.ColumnUnreadable || resp.Time.Timed != 60 || resp.BucketSeconds != 3600 {
		t.Fatalf("time column: time = %+v, bucket = %d", resp.Time, resp.BucketSeconds)
	}
	// The choice is remembered for the service.
	a, err = d.analyze(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if a.resp.Time.Column != "when" || a.resp.BucketSeconds == 0 {
		t.Fatalf("reloaded: time = %+v", a.resp.Time)
	}
}

func TestTimeSourceFacts(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	ctx := context.Background()
	analyzeText := func(name, text string) demoPatternsResponse {
		t.Helper()
		id, err := d.importLog(ctx, name, strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		a, err := d.analyze(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return a.resp
	}

	var js strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&js, `{"msg":"job %d done","at":"2026-10-25 10:%02d:00"}`+"\n", i, i)
	}
	if got := analyzeText("jobs.log", js.String()).Time; got.Format != timeFormatJSON || !slices.Equal(got.Fields, []string{"_time", "ts"}) || !slices.Equal(got.Columns, []string{"msg", "at"}) {
		t.Fatalf("json: time = %+v", got)
	}

	var txt strings.Builder
	// The file doesn't open with a time, so each line stays its own entry.
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&txt, "worker tick %d\n", i)
	}
	txt.WriteString("2026-10-25T10:00:00Z worker stopped\n")
	resp := analyzeText("worker.log", txt.String())
	if got := resp.Time; got.Format != timeFormatText || got.Timed != 1 || got.Lines != 301 || got.Columns != nil || resp.BucketSeconds != 0 {
		t.Fatalf("partial text: time = %+v, bucket = %d", got, resp.BucketSeconds)
	}
}

// chooseTimeColumn posts a time column choice and returns the response.
func chooseTimeColumn(t *testing.T, mux *http.ServeMux, id, column string) demoPatternsResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/demo/time-column?ws="+id, strings.NewReader(fmt.Sprintf(`{"column":%q}`, column))))
	if rec.Code != http.StatusOK {
		t.Fatalf("choose %s: %d %s", column, rec.Code, rec.Body)
	}
	var resp demoPatternsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestTimeColumnTwoColumnCSV checks the picker for a plain two-column CSV
// whose time column lapp does not recognize at line starts (Unix ms).
func TestTimeColumnTwoColumnCSV(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	mux := http.NewServeMux()
	d.register(mux)
	base := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString("when,msg\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "%d,request served %d\n", base.Add(time.Duration(i)*time.Hour).UnixMilli(), i)
	}
	id, err := d.importLog(context.Background(), "requests.csv", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.analyze(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.resp.Time; got.Format != timeFormatCSV || !slices.Equal(got.Columns, []string{"when", "msg"}) || a.resp.BucketSeconds != 0 {
		t.Fatalf("before choosing: time = %+v, bucket = %d", got, a.resp.BucketSeconds)
	}
	resp := chooseTimeColumn(t, mux, id, "when")
	// The header row is not a log entry: 40 lines, all timed, none unmatched.
	if resp.Time.Timed != 40 || resp.Time.Lines != 40 || resp.Workspace.LineCount != 40 || resp.Unmatched.Count != 0 || resp.BucketSeconds != 3600 || !resp.From.Equal(base) {
		t.Fatalf("after choosing: time = %+v, lines = %d, unmatched = %d, bucket = %d, from = %v", resp.Time, resp.Workspace.LineCount, resp.Unmatched.Count, resp.BucketSeconds, resp.From)
	}
}

// TestTimeColumnMultilineCSV checks that a quoted field spanning lines keeps
// the columns intact and that every line of a record gets the record's time.
func TestTimeColumnMultilineCSV(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	mux := http.NewServeMux()
	d.register(mux)
	base := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString("ts,level,msg\r\n")
	for i := 0; i < 30; i++ {
		msg := fmt.Sprintf("job %d done", i)
		if i%3 == 0 {
			msg = fmt.Sprintf("\"job %d failed:\r\n  at step, \"\"load\"\"\r\n  retrying\"", i)
		}
		fmt.Fprintf(&b, "%d,info,%s\r\n", base.Add(time.Duration(i)*time.Minute).Unix(), msg)
		if i == 10 {
			b.WriteString("\r\n")
		}
	}
	id, err := d.importLog(context.Background(), "jobs.csv", strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.analyze(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.resp.Time; got.Format != timeFormatCSV || !slices.Equal(got.Columns, []string{"ts", "level", "msg"}) {
		t.Fatalf("before choosing: time = %+v", got)
	}
	resp := chooseTimeColumn(t, mux, id, "ts")
	// Every line has a time: 30 records, 10 of them three lines; the header
	// row is not counted.
	if resp.Time.Lines != 50 || resp.Time.Timed != 50 || resp.Workspace.LineCount != 50 || !resp.From.Equal(base) || !resp.To.Equal(base.Add(29*time.Minute)) {
		t.Fatalf("after choosing: time = %+v, from = %v, to = %v", resp.Time, resp.From, resp.To)
	}
}

// TestTimeColumnKeepsNoiseDecisions checks that saving the time column does
// not drop noise decisions in the same rules file.
func TestTimeColumnKeepsNoiseDecisions(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	if err := d.updateRules("orders", func(r *noiseRules) { r.Filtered = []string{"a"}; r.NotNoise = []string{"b"} }); err != nil {
		t.Fatal(err)
	}
	if err := d.updateRules("orders", func(r *noiseRules) { r.TimeColumn = "when" }); err != nil {
		t.Fatal(err)
	}
	rules, _ := d.loadRules("orders")
	if !slices.Equal(rules.Filtered, []string{"a"}) || !slices.Equal(rules.NotNoise, []string{"b"}) || rules.TimeColumn != "when" {
		t.Fatalf("rules = %+v", rules)
	}
}

// TestCommaTextKeepsFirstLine checks that a plain log whose lines happen to
// contain one comma keeps its first line while no time column is chosen.
func TestCommaTextKeepsFirstLine(t *testing.T) {
	d := newDemoAPI(filepath.Join(t.TempDir(), "workspaces"), "", "")
	text := "worker started,job ready\nworker running,job active\nworker stopped,job done\n"
	id, err := d.importLog(context.Background(), "worker.log", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.analyze(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.lines) != 3 || a.resp.Workspace.LineCount != 3 || a.resp.Time.Lines != 3 {
		t.Fatalf("lines = %d, lineCount = %d, time = %+v", len(a.lines), a.resp.Workspace.LineCount, a.resp.Time)
	}
}
