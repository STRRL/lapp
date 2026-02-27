package loghub

import (
	"encoding/csv"
	"os"

	"github.com/go-errors/errors"
)

// LogEntry represents a single parsed log entry from a Loghub CSV file.
type LogEntry struct {
	Content       string
	EventTemplate string
	EventID       string
}

// LoadDataset reads a Loghub structured CSV file and returns parsed entries.
// Column indices are determined dynamically from the header row.
func LoadDataset(csvPath string) ([]LogEntry, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, errors.Errorf("open csv: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, errors.Errorf("read csv: %w", err)
	}

	if len(records) < 2 {
		return nil, errors.Errorf("csv has fewer than 2 rows (header + data)")
	}

	header := records[0]
	colContent := -1
	colTemplate := -1
	colEventID := -1

	for i, name := range header {
		switch name {
		case "Content":
			colContent = i
		case "EventTemplate":
			colTemplate = i
		case "EventId":
			colEventID = i
		}
	}

	if colContent == -1 {
		return nil, errors.Errorf("missing required column: Content")
	}
	if colTemplate == -1 {
		return nil, errors.Errorf("missing required column: EventTemplate")
	}
	if colEventID == -1 {
		return nil, errors.Errorf("missing required column: EventId")
	}

	entries := make([]LogEntry, 0, len(records)-1)
	for _, row := range records[1:] {
		if len(row) <= colContent || len(row) <= colTemplate || len(row) <= colEventID {
			continue
		}
		entries = append(entries, LogEntry{
			Content:       row[colContent],
			EventTemplate: row[colTemplate],
			EventID:       row[colEventID],
		})
	}

	return entries, nil
}
