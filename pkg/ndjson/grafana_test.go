package ndjson

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNormalizeFixtures normalizes every input fixture under
// testdata/normalize/ and compares the result against its committed expected
// output. Each <case>.input.<ext> pairs with <case>.expected.txt; content
// that is not a Grafana export is expected back unchanged.
func TestNormalizeFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "normalize", "*.input.*"))
	if err != nil {
		t.Fatalf("glob normalize fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no normalize fixtures found under testdata/normalize")
	}

	for _, inputPath := range inputs {
		name, _, _ := strings.Cut(filepath.Base(inputPath), ".input.")
		expectedPath := filepath.Join("testdata", "normalize", name+".expected.txt")
		t.Run(name, func(t *testing.T) {
			got := normalizeFile(t, inputPath)
			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected output %s: %v", expectedPath, err)
			}
			if got != string(expected) {
				t.Fatalf("normalize mismatch for %s\ngot:\n%swant:\n%s", inputPath, got, expected)
			}
		})
	}
}

// TestNormalizeGrafanaExportFeedsNDJSONPath checks that converted exports
// are detected as NDJSON and extract oldest-first timestamps, keeping the
// nanosecond precision of the JSON export timestamp.
func TestNormalizeGrafanaExportFeedsNDJSONPath(t *testing.T) {
	tests := []struct {
		fixture string
		want    []string
	}{
		{
			fixture: "grafana-json-newest-first.input.json",
			want:    []string{"2026-10-26T05:41:30Z", "2026-10-26T05:41:40Z", "2026-10-26T05:41:50.000000123Z", "2026-10-26T05:42:00.5Z"},
		},
		{
			fixture: "grafana-csv-multiline.input.csv",
			want:    []string{"2026-10-26T05:41:40Z", "2026-10-26T05:41:50Z", "2026-10-26T05:42:00.5Z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			normalized := normalizeFile(t, filepath.Join("testdata", "normalize", tt.fixture))
			lines := strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
			if got := DetectFormat(lines); got != FormatNDJSON {
				t.Fatalf("DetectFormat = %q, want %q", got, FormatNDJSON)
			}
			var got []string
			for _, line := range lines {
				_, ts := Extract(line)
				got = append(got, ts.Format(time.RFC3339Nano))
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("timestamps = %v, want %v", got, tt.want)
			}
		})
	}
}

func normalizeFile(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	normalized, err := Normalize(file)
	if err != nil {
		t.Fatalf("Normalize(%s): %v", path, err)
	}
	data, err := io.ReadAll(normalized)
	if err != nil {
		t.Fatalf("read normalized %s: %v", path, err)
	}
	return string(data)
}
