package ndjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectFormatFixtures classifies every fixture under testdata/detect/.
// The expected format is encoded in the file name as <case>.expect-<format>.log,
// so adding a case only means adding one fixture file.
func TestDetectFormatFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "detect", "*.log"))
	if err != nil {
		t.Fatalf("glob detect fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no detect fixtures found under testdata/detect")
	}

	for _, path := range paths {
		name, want := parseDetectFixtureName(t, path)
		t.Run(name, func(t *testing.T) {
			got := DetectFormat(readFixtureLines(t, path))
			if got != want {
				t.Fatalf("DetectFormat(%s) = %q, want %q", path, got, want)
			}
		})
	}
}

func TestDetectFormatEmptyInput(t *testing.T) {
	if got := DetectFormat(nil); got != FormatText {
		t.Fatalf("DetectFormat(nil) = %q, want %q", got, FormatText)
	}
}

func parseDetectFixtureName(t *testing.T, path string) (string, Format) {
	t.Helper()
	base := strings.TrimSuffix(filepath.Base(path), ".log")
	name, expected, ok := strings.Cut(base, ".expect-")
	if !ok {
		t.Fatalf("detect fixture %s must be named <case>.expect-<format>.log", path)
	}
	format := Format(expected)
	if format != FormatText && format != FormatNDJSON {
		t.Fatalf("detect fixture %s has unknown expected format %q", path, expected)
	}
	return name, format
}

func readFixtureLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}
