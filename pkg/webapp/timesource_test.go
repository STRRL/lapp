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

	choose := func(column string) demoPatternsResponse {
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
