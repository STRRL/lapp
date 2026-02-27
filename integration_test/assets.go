package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/strrl/lapp/pkg/parser"
	"github.com/strrl/lapp/pkg/store"
)

// loghubPath returns the LOGHUB_PATH env var or skips the test.
func loghubPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("LOGHUB_PATH")
	if p == "" {
		t.Skip("LOGHUB_PATH not set, skipping integration test")
	}
	return p
}

// newDrainParser creates a fresh DrainParser.
// Each call returns independent state (important because DrainParser is stateful).
func newDrainParser(t *testing.T) *parser.DrainParser {
	t.Helper()
	drainParser, err := parser.NewDrainParser()
	if err != nil {
		t.Fatalf("create drain parser: %v", err)
	}
	return drainParser
}

// newStore creates a fresh in-memory DuckDB store with cleanup registered.
func newStore(t *testing.T) *store.DuckDBStore {
	t.Helper()
	s, err := store.NewDuckDBStore("")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return s
}

// outputDir returns the directory for saving template results.
// Set TEMPLATE_OUTPUT_DIR to customize; defaults to a mktemp dir.
// The directory path is logged so you can find the output.
func outputDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TEMPLATE_OUTPUT_DIR")
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "lapp-integration-templates-*")
		if err != nil {
			t.Fatalf("create temp dir: %v", err)
		}
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create output dir: %v", err)
		}
	}
	t.Logf("Template output dir: %s", dir)
	return dir
}

// templateResult is the JSON structure saved per dataset.
type templateResult struct {
	Dataset       string            `json:"dataset"`
	TestPath      string            `json:"test_path"`
	TotalEntries  int               `json:"total_entries"`
	TemplateCount int               `json:"template_count"`
	Templates     []templateSummary `json:"templates"`
}

type templateSummary struct {
	PatternID string `json:"pattern_id"`
	Pattern   string `json:"pattern"`
	Count     int    `json:"count"`
}

// saveTemplates writes template summaries to a JSON file in the output directory.
func saveTemplates(t *testing.T, dir string, result templateResult) {
	t.Helper()
	filename := result.Dataset + "_" + result.TestPath + ".json"
	path := filepath.Join(dir, filename)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal templates: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write templates: %v", err)
	}
	t.Logf("Saved %d templates to %s", result.TemplateCount, path)
}
