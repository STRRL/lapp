package ndjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExtractFixtures extracts every line of every input fixture under
// testdata/extract/ and compares the result against its committed expected
// output. Each <case>.input.ndjson pairs with <case>.expected.txt, so adding
// a case only means adding one fixture pair. An expected line is the
// severity, a tab, then the extracted text.
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
				text, severity, _ := Extract(line)
				extracted = append(extracted, severity+"\t"+text)
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
	if got, _, _ := Extract(line); got != line {
		t.Fatalf("Extract(%q) = %q, want unchanged", line, got)
	}
}

func TestExtractTimestampFromVictoriaLogsTime(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "_time is read", line: `{"_msg":"ok","_time":"2026-10-26T05:22:22Z"}`, want: "2026-10-26T05:22:22Z"},
		{name: "a user ts field does not override _time", line: `{"_msg":"ok","_time":"2026-10-26T05:22:22Z","ts":"2020-01-01T00:00:00Z"}`, want: "2026-10-26T05:22:22Z"},
		{name: "GCP envelope ts is read", line: `{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"a"}}`, want: "2026-07-25T00:00:01Z"},
		{name: "no timestamp field gives zero time", line: `{"message":"ok"}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ts := Extract(tt.line)
			got := ""
			if !ts.IsZero() {
				got = ts.UTC().Format(time.RFC3339)
			}
			if got != tt.want {
				t.Fatalf("timestamp = %q, want %q", got, tt.want)
			}
		})
	}
}
