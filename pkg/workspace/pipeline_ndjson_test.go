package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strrl/lapp/pkg/semantic"
)

func TestDiscoverMixedTextAndNDJSONWorkspace(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	textLines := []string{
		"2026-06-06 10:00:00 ERROR db timeout user=42",
		"2026-06-06 10:00:01 ERROR db timeout user=43",
	}
	mustWrite(t, filepath.Join(dir, "logs", "app.log"), strings.Join(textLines, "\n")+"\n")
	ndjsonLines := []string{
		`{"ts":"2026-06-06T10:00:02Z","severity":"ERROR","payload":{"message":"payment declined order=1001"}}`,
		`{"ts":"2026-06-06T10:00:03Z","severity":"ERROR","payload":{"message":"payment declined order=1002"}}`,
	}
	unmatchedLine := `{"status_code":901,"details":{"state":"only_once_marker"}}`
	mustWrite(t, filepath.Join(dir, "logs", "service.ndjson"), strings.Join(append(append([]string{}, ndjsonLines...), unmatchedLine), "\n")+"\n")

	result, err := Discover(context.Background(), DiscoveryConfig{
		Dir:   dir,
		RunID: "01900000-0000-7000-8000-000000000002",
		Labeler: func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			labels := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				labels = append(labels, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "pattern-" + input.PatternUUIDString[:8],
					Description:       "Labeled by test",
				})
			}
			return labels, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if result.FileCount != 2 || result.LineCount != 5 || result.PatternCount != 2 || result.UnmatchedCount != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	record, err := ReadDiscoveryRunRecord(dir, result.RunID)
	if err != nil {
		t.Fatalf("ReadDiscoveryRunRecord: %v", err)
	}

	textPattern := findPatternForFile(record.Patterns, "app.log")
	if textPattern == nil {
		t.Fatalf("expected a pattern originating from app.log, got %+v", record.Patterns)
	}
	jsonPattern := findPatternForFile(record.Patterns, "service.ndjson")
	if jsonPattern == nil {
		t.Fatalf("expected a pattern originating from service.ndjson, got %+v", record.Patterns)
	}

	runDir := DiscoveryRunDir(dir, result.RunID)
	textSamples := mustRead(t, filepath.Join(runDir, "patterns", textPattern.DirName, "samples.log"))
	assertContains(t, textSamples, "ERROR db timeout user=42")

	jsonSamples := mustRead(t, filepath.Join(runDir, "patterns", jsonPattern.DirName, "samples.log"))
	sampleLines := strings.Split(strings.TrimSuffix(jsonSamples, "\n"), "\n")
	if len(sampleLines) != len(ndjsonLines) {
		t.Fatalf("expected %d NDJSON samples, got %d:\n%s", len(ndjsonLines), len(sampleLines), jsonSamples)
	}
	for i, line := range sampleLines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("sample line %d is not a JSON object: %v\nline: %s", i+1, err, line)
		}
		if line != ndjsonLines[i] {
			t.Fatalf("sample line %d is not the raw NDJSON line\ngot:  %s\nwant: %s", i+1, line, ndjsonLines[i])
		}
	}

	unmatchedSamples := mustRead(t, filepath.Join(runDir, "patterns", "unmatched", "samples.log"))
	assertContains(t, unmatchedSamples, unmatchedLine)
}

func findPatternForFile(patterns []PatternInfo, fileName string) *PatternInfo {
	for i := range patterns {
		for _, ref := range patterns[i].LineRefs {
			if ref.FileName == fileName {
				return &patterns[i]
			}
		}
	}
	return nil
}
