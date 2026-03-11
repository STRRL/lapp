package event

import (
	"path/filepath"
	"testing"
)

func TestLoadFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "fixtures", "events", "v1")

	fixtures, err := LoadFixtures(dir)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	if len(fixtures) != 4 {
		t.Fatalf("expected 4 fixtures, got %d", len(fixtures))
	}

	seen := make(map[string]bool, len(fixtures))
	for _, fixture := range fixtures {
		seen[fixture.SourceFormat] = true
	}

	required := []string{
		SourceFormatJSON,
		SourceFormatLogfmt,
		SourceFormatKeyValue,
		SourceFormatPlainText,
	}
	for _, format := range required {
		if !seen[format] {
			t.Fatalf("missing fixture for %s", format)
		}
	}
}
