package gcplog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConvertEntryFixtures converts every GCP entry fixture under
// testdata/convert/ and compares the result against its committed expected
// envelope line. Each <case>.input.json pairs with <case>.expected.txt, so
// adding a case only means adding one fixture pair.
func TestConvertEntryFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "convert", "*.input.json"))
	if err != nil {
		t.Fatalf("glob convert fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no convert fixtures found under testdata/convert")
	}

	for _, inputPath := range inputs {
		name := strings.TrimSuffix(filepath.Base(inputPath), ".input.json")
		expectedPath := filepath.Join("testdata", "convert", name+".expected.txt")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("read input fixture %s: %v", inputPath, err)
			}
			var entry Entry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Fatalf("decode input fixture %s: %v", inputPath, err)
			}

			line, err := ConvertEntry(entry)
			if err != nil {
				t.Fatalf("ConvertEntry: %v", err)
			}
			got := line + "\n"

			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected output %s: %v", expectedPath, err)
			}
			if got != string(expected) {
				t.Fatalf("conversion mismatch for %s\ngot:\n%swant:\n%s", inputPath, got, expected)
			}
		})
	}
}

func TestCombinedFilterJoinsTimeRangeAndFilter(t *testing.T) {
	from := time.Date(2026, 7, 25, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	to := time.Date(2026, 7, 25, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got := CombinedFilter(`resource.type="cloud_run_revision"`, from, to)
	want := `timestamp >= "2026-07-25T00:00:00Z" AND timestamp <= "2026-07-25T01:00:00Z" AND (resource.type="cloud_run_revision")`
	if got != want {
		t.Fatalf("CombinedFilter with filter = %q, want %q", got, want)
	}

	got = CombinedFilter("", from, to)
	want = `timestamp >= "2026-07-25T00:00:00Z" AND timestamp <= "2026-07-25T01:00:00Z"`
	if got != want {
		t.Fatalf("CombinedFilter without filter = %q, want %q", got, want)
	}
}

func TestCombinedFilterKeepsFractionalSeconds(t *testing.T) {
	from := time.Date(2026, 7, 25, 0, 0, 0, 250_000_000, time.UTC)
	to := time.Date(2026, 7, 25, 1, 0, 0, 123_456_789, time.FixedZone("EDT", -4*3600))

	got := CombinedFilter("", from, to)
	want := `timestamp >= "2026-07-25T00:00:00.25Z" AND timestamp <= "2026-07-25T05:00:00.123456789Z"`
	if got != want {
		t.Fatalf("CombinedFilter fractional bounds = %q, want %q", got, want)
	}
}
