package webapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticFileHandlerUsesStaticDirAndFallsBackToIndex(t *testing.T) {
	staticDir := t.TempDir()
	mustWriteFile(t, filepath.Join(staticDir, "index.html"), "<!doctype html><div id=\"root\">app</div>")
	mustWriteFile(t, filepath.Join(staticDir, "assets", "app.js"), "console.log('app')")

	handler, err := staticFileHandler(staticDir)
	if err != nil {
		t.Fatalf("staticFileHandler: %v", err)
	}

	response := recordRequest(handler, "/assets/app.js")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "console.log") {
		t.Fatalf("expected asset response, got status=%d body=%q", response.Code, response.Body.String())
	}

	response = recordRequest(handler, "/workspaces/payment-timeout")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="root"`) {
		t.Fatalf("expected SPA index fallback, got status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestStaticFileHandlerPrefersBuiltAppIndex(t *testing.T) {
	staticDir := t.TempDir()
	mustWriteFile(t, filepath.Join(staticDir, "index.html"), "<!doctype html><div>fallback</div>")
	mustWriteFile(t, filepath.Join(staticDir, "app", "index.html"), "<!doctype html><div id=\"root\"></div>")
	mustWriteFile(t, filepath.Join(staticDir, "app", "assets", "app.js"), "console.log('built')")

	handler, err := staticFileHandler(staticDir)
	if err != nil {
		t.Fatalf("staticFileHandler: %v", err)
	}

	response := recordRequest(handler, "/")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="root"`) {
		t.Fatalf("expected built app index, got status=%d body=%q", response.Code, response.Body.String())
	}

	response = recordRequest(handler, "/workspaces/payment-timeout")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="root"`) {
		t.Fatalf("expected built app SPA fallback, got status=%d body=%q", response.Code, response.Body.String())
	}

	response = recordRequest(handler, "/app/assets/app.js")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "built") {
		t.Fatalf("expected built app asset, got status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestStaticFileHandlerUsesEmbeddedFallback(t *testing.T) {
	handler, err := staticFileHandler("")
	if err != nil {
		t.Fatalf("staticFileHandler: %v", err)
	}

	response := recordRequest(handler, "/")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "LAPP") {
		t.Fatalf("expected embedded fallback index, got status=%d body=%q", response.Code, response.Body.String())
	}
}

func recordRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
