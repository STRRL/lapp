package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/strrl/lapp/pkg/pattern"
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
	if record.Progress == nil || record.Progress.Step != DiscoveryStepWritingResults {
		t.Fatalf("expected structured discovery progress, got %+v", record.Progress)
	}
}

func TestLabelPatternsBatchesLargeTemplateSets(t *testing.T) {
	var templates []pattern.DrainCluster
	var lines []string
	for i := range 325 {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("pattern-%d", i)))
		templates = append(templates, pattern.DrainCluster{
			ID:      id,
			Pattern: "ERROR request <*> failed",
			Count:   2,
		})
		lines = append(lines, "ERROR request abc failed")
	}

	var mu sync.Mutex
	var batchSizes []int
	var progressMessages []string
	active := 0
	maxActive := 0
	labels, err := labelPatterns(
		context.Background(),
		func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			mu.Lock()
			batchSizes = append(batchSizes, len(inputs))
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()

			result := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				result = append(result, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "request-failed",
					Description:       "Request failed",
				})
			}
			return result, nil
		},
		DiscoveryConfig{},
		templates,
		lines,
		func(progress labelProgress) error {
			progressMessages = append(progressMessages, fmt.Sprintf("%s:%d/%d:%d:%d", progress.Event, progress.BatchNumber, progress.BatchCount, progress.BatchSize, progress.CompletedCount))
			return nil
		},
	)
	if err != nil {
		t.Fatalf("labelPatterns: %v", err)
	}
	if len(labels) != 325 {
		t.Fatalf("expected 325 labels, got %d", len(labels))
	}
	sort.Ints(batchSizes)
	if len(batchSizes) != 13 {
		t.Fatalf("expected 13 batches, got %v", batchSizes)
	}
	for _, size := range batchSizes {
		if size != defaultLabelBatchSize {
			t.Fatalf("expected all batches to have %d inputs, got %v", defaultLabelBatchSize, batchSizes)
		}
	}
	if maxActive <= 1 || maxActive > defaultLabelConcurrency {
		t.Fatalf("expected concurrent labeling capped at %d, got max active %d", defaultLabelConcurrency, maxActive)
	}
	if len(progressMessages) != 26 {
		t.Fatalf("expected start and completion progress for 13 batches, got %d messages: %v", len(progressMessages), progressMessages)
	}
}

func TestLabelPatternsRetriesFailedBatch(t *testing.T) {
	withNoLabelRetryDelay(t)

	var templates []pattern.DrainCluster
	var lines []string
	for i := range 30 {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("retry-pattern-%d", i)))
		templates = append(templates, pattern.DrainCluster{
			ID:      id,
			Pattern: "ERROR retry <*> failed",
			Count:   2,
		})
		lines = append(lines, "ERROR retry abc failed")
	}

	var mu sync.Mutex
	calls := 0
	var progressEvents []labelProgressEvent
	labels, err := labelPatterns(
		context.Background(),
		func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			mu.Lock()
			calls++
			call := calls
			mu.Unlock()
			if call == 1 {
				return nil, errors.New("parse LLM response: invalid JSON")
			}

			result := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				result = append(result, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "retry-failed",
					Description:       "Retryable pattern",
				})
			}
			return result, nil
		},
		DiscoveryConfig{LabelConcurrency: 1},
		templates,
		lines,
		func(progress labelProgress) error {
			progressEvents = append(progressEvents, progress.Event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("labelPatterns: %v", err)
	}
	if len(labels) != 30 {
		t.Fatalf("expected 30 labels after retry, got %d", len(labels))
	}
	if calls != 3 {
		t.Fatalf("expected 3 labeler calls for two batches with one retry, got %d", calls)
	}
	if !hasLabelProgressEvent(progressEvents, labelProgressRetrying) {
		t.Fatalf("expected retry progress event, got %v", progressEvents)
	}
}

func TestLabelPatternsFailsAfterRetryExhausted(t *testing.T) {
	withNoLabelRetryDelay(t)

	var templates []pattern.DrainCluster
	var lines []string
	for i := range 2 {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("exhausted-pattern-%d", i)))
		templates = append(templates, pattern.DrainCluster{
			ID:      id,
			Pattern: "ERROR exhausted <*> failed",
			Count:   2,
		})
		lines = append(lines, "ERROR exhausted abc failed")
	}

	calls := 0
	_, err := labelPatterns(
		context.Background(),
		func(_ context.Context, _ semantic.Config, _ []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			calls++
			return nil, errors.New("parse LLM response: invalid JSON")
		},
		DiscoveryConfig{LabelConcurrency: 1, LabelMaxAttempts: 2},
		templates,
		lines,
		nil,
	)
	if err == nil {
		t.Fatal("expected retry exhaustion error")
	}
	if calls != 2 {
		t.Fatalf("expected 2 labeler calls, got %d", calls)
	}
	assertContains(t, err.Error(), "label batch 1/1 failed after 2 attempts")
	assertContains(t, err.Error(), "parse LLM response: invalid JSON")
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

func withNoLabelRetryDelay(t *testing.T) {
	t.Helper()
	original := labelRetryDelay
	labelRetryDelay = func(int) time.Duration {
		return 0
	}
	t.Cleanup(func() {
		labelRetryDelay = original
	})
}

func hasLabelProgressEvent(events []labelProgressEvent, want labelProgressEvent) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
