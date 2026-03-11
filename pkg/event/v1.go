package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	goerrors "github.com/go-errors/errors"
)

const (
	SourceFormatJSON      = "json"
	SourceFormatLogfmt    = "logfmt"
	SourceFormatKeyValue  = "key_value"
	SourceFormatPlainText = "plain_text"
)

var allowedSourceFormats = map[string]struct{}{
	SourceFormatJSON:      {},
	SourceFormatLogfmt:    {},
	SourceFormatKeyValue:  {},
	SourceFormatPlainText: {},
}

// Event is the v1 normalized log event shape.
type Event struct {
	Timestamp *time.Time        `json:"ts,omitempty"`
	Text      string            `json:"text"`
	Attrs     map[string]string `json:"attrs"`
	Inferred  *Inferred         `json:"inferred"`
}

// Inferred contains metadata derived after parsing.
type Inferred struct {
	Pattern string `json:"pattern,omitempty"`
	Entity  string `json:"entity,omitempty"`
}

// Fixture wraps an example event with provenance metadata.
type Fixture struct {
	Name         string `json:"name"`
	SourceFormat string `json:"source_format"`
	Description  string `json:"description"`
	Event        Event  `json:"event"`
}

// Validate checks that the fixture satisfies the documented v1 contract.
func (f Fixture) Validate() error {
	if strings.TrimSpace(f.Name) == "" {
		return goerrors.New("name is required")
	}
	if strings.TrimSpace(f.Description) == "" {
		return goerrors.New("description is required")
	}
	if _, ok := allowedSourceFormats[f.SourceFormat]; !ok {
		return goerrors.Errorf("validate source_format: expected one of %q, %q, %q, %q", SourceFormatJSON, SourceFormatLogfmt, SourceFormatKeyValue, SourceFormatPlainText)
	}
	if strings.TrimSpace(f.Event.Text) == "" {
		return goerrors.New("event.text is required")
	}
	if f.Event.Attrs == nil {
		return goerrors.New("event.attrs is required")
	}
	if f.Event.Inferred == nil {
		return goerrors.New("event.inferred is required")
	}
	for key := range f.Event.Attrs {
		if strings.TrimSpace(key) == "" {
			return goerrors.New("event.attrs contains an empty key")
		}
	}
	return nil
}

// LoadFixtures reads all JSON fixture files from a directory.
func LoadFixtures(dir string) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, goerrors.Errorf("read fixtures dir: %w", err)
	}

	fixtures := make([]Fixture, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, goerrors.Errorf("read fixture %s: %w", path, err)
		}

		var fixture Fixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			return nil, goerrors.Errorf("decode fixture %s: %w", path, err)
		}
		if err := fixture.Validate(); err != nil {
			return nil, goerrors.Errorf("validate fixture %s: %w", path, err)
		}

		fixtures = append(fixtures, fixture)
	}

	return fixtures, nil
}
