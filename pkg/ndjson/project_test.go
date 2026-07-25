package ndjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectFixtures projects every line of every input fixture under
// testdata/project/ and compares the result against its committed expected
// output. Each <case>.input.ndjson pairs with <case>.expected.txt, so adding
// a case only means adding one fixture pair.
func TestProjectFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "project", "*.input.ndjson"))
	if err != nil {
		t.Fatalf("glob projection fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no projection fixtures found under testdata/project")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.ndjson")
		expectedPath := filepath.Join("testdata", "project", name+".expected.txt")
		t.Run(name, func(t *testing.T) {
			var projected []string
			for _, line := range readFixtureLines(t, inputPath) {
				if strings.TrimSpace(line) == "" {
					continue
				}
				projected = append(projected, Project(line))
			}
			got := strings.Join(projected, "\n") + "\n"

			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected output %s: %v", expectedPath, err)
			}
			if got != string(expected) {
				t.Fatalf("projection mismatch for %s\ngot:\n%swant:\n%s", inputPath, got, expected)
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
