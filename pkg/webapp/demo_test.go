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
