package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strrl/lapp/pkg/semantic"
)

func TestDiscoverGeneratesRunScopedResultsFromLogs(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	mustMkdir(t, filepath.Join(dir, "patterns", "stale"))
	mustMkdir(t, filepath.Join(dir, "notes"))
	mustWrite(t, filepath.Join(dir, "patterns", "stale", "samples.log"), "old\n")
	mustWrite(t, filepath.Join(dir, "notes", "summary.md"), "old\n")
	mustWrite(t, filepath.Join(dir, "logs", "app.log"), strings.Join([]string{
		"2026-06-06 10:00:00 INFO server started port=8080",
		"2026-06-06 10:00:01 ERROR db timeout user=42",
		"2026-06-06 10:00:02 ERROR db timeout user=43",
	}, "\n")+"\n")

	var labelInputs []semantic.PatternInput
	result, err := Discover(context.Background(), DiscoveryConfig{
		Dir:   dir,
		RunID: "01900000-0000-7000-8000-000000000001",
		Labeler: func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			labelInputs = append(labelInputs, inputs...)
			labels := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				labels = append(labels, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "db-timeout-error",
					Description:       "Database timeout for a user request",
				})
			}
			return labels, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if result.FileCount != 1 || result.LineCount != 3 || result.PatternCount != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(labelInputs) != 1 {
		t.Fatalf("expected one label input, got %d", len(labelInputs))
	}
	if _, err := os.Stat(filepath.Join(dir, "patterns", "stale")); err != nil {
		t.Fatalf("expected legacy top-level pattern dir to be left alone, stat err=%v", err)
	}

	runDir := DiscoveryRunDir(dir, result.RunID)
	summary := mustRead(t, filepath.Join(runDir, "notes", "summary.md"))
	assertContains(t, summary, "db-timeout-error")
	assertContains(t, summary, "**Unmatched lines:** 1")

	errorsMD := mustRead(t, filepath.Join(runDir, "notes", "errors.md"))
	assertContains(t, errorsMD, "db-timeout-error")

	samples := mustRead(t, filepath.Join(runDir, "patterns", "db-timeout-error", "samples.log"))
	assertContains(t, samples, "ERROR db timeout user=42")

	unmatched := mustRead(t, filepath.Join(runDir, "patterns", "unmatched", "samples.log"))
	assertContains(t, unmatched, "INFO server started")

	record, err := ReadDiscoveryRunRecord(dir, result.RunID)
	if err != nil {
		t.Fatalf("ReadDiscoveryRunRecord: %v", err)
	}
	if record.State != DiscoveryRunStateSucceeded || record.PatternCount != 1 || record.UnmatchedCount != 1 {
		t.Fatalf("unexpected discovery run record: %+v", record)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertContains(t *testing.T, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("expected content to contain %q, got:\n%s", want, content)
	}
}
