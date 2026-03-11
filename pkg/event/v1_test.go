package event

import (
	"path/filepath"
	"testing"
)

func TestLoadFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "fixtures", "events", "v1")
	required := []string{
		SourceFormatJSON,
		SourceFormatLogfmt,
		SourceFormatKeyValue,
		SourceFormatPlainText,
	}

	fixtures, err := LoadFixtures(dir)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	if len(fixtures) != len(required) {
		t.Fatalf("expected %d fixtures, got %d", len(required), len(fixtures))
	}

	seen := make(map[string]bool, len(required))
	for _, fixture := range fixtures {
		seen[fixture.SourceFormat] = true
	}

	for _, format := range required {
		if !seen[format] {
			t.Fatalf("missing fixture for %s", format)
		}
	}
}
