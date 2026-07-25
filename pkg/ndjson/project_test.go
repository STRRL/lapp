package ndjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectGoldenFixtures projects every line of every input fixture under
// testdata/project/ and compares the result against its committed golden
// file. Each <case>.input.ndjson pairs with <case>.golden, so adding a case
// only means adding one fixture pair.
func TestProjectGoldenFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "project", "*.input.ndjson"))
	if err != nil {
		t.Fatalf("glob projection fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no projection fixtures found under testdata/project")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.ndjson")
		goldenPath := filepath.Join("testdata", "project", name+".golden")
		t.Run(name, func(t *testing.T) {
			var projected []string
			for _, line := range readFixtureLines(t, inputPath) {
				if strings.TrimSpace(line) == "" {
					continue
				}
				projected = append(projected, Project(line))
			}
			got := strings.Join(projected, "\n") + "\n"

			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}
			if got != string(golden) {
				t.Fatalf("projection mismatch for %s\ngot:\n%swant:\n%s", inputPath, got, golden)
			}
		})
	}
}

func TestProjectNonJSONLineIsReturnedUnchanged(t *testing.T) {
	line := "2026-06-06 10:00:00 INFO plain text line"
	if got := Project(line); got != line {
		t.Fatalf("Project(%q) = %q, want unchanged", line, got)
	}
}
