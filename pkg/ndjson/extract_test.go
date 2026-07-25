package ndjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractFixtures extracts every line of every input fixture under
// testdata/extract/ and compares the result against its committed expected
// output. Each <case>.input.ndjson pairs with <case>.expected.txt, so adding
// a case only means adding one fixture pair.
func TestExtractFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "extract", "*.input.ndjson"))
	if err != nil {
		t.Fatalf("glob extraction fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no extraction fixtures found under testdata/extract")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.ndjson")
		expectedPath := filepath.Join("testdata", "extract", name+".expected.txt")
		t.Run(name, func(t *testing.T) {
			var extracted []string
			for _, line := range readFixtureLines(t, inputPath) {
				if strings.TrimSpace(line) == "" {
					continue
				}
				extracted = append(extracted, Extract(line))
			}
			got := strings.Join(extracted, "\n") + "\n"

			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected output %s: %v", expectedPath, err)
			}
			if got != string(expected) {
				t.Fatalf("extraction mismatch for %s\ngot:\n%swant:\n%s", inputPath, got, expected)
			}
		})
	}
}

func TestExtractNonJSONLineIsReturnedUnchanged(t *testing.T) {
	line := "2026-06-06 10:00:00 INFO plain text line"
	if got := Extract(line); got != line {
		t.Fatalf("Extract(%q) = %q, want unchanged", line, got)
	}
}
