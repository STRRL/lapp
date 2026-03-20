package tape

import (
	"encoding/json"
	"os"
	"sync"
)

// Recorder is the interface for appending tape entries.
type Recorder interface {
	Append(entry Entry) error
}

var _ Recorder = (*JSONLStore)(nil)

// JSONLStore is an append-only tape store that writes entries as JSONL to a file.
type JSONLStore struct {
	mu     sync.Mutex
	path   string
	nextID int
}

// NewJSONLStore creates a new JSONL store writing to the given file path.
func NewJSONLStore(path string) *JSONLStore {
	return &JSONLStore{
		path:   path,
		nextID: 1,
	}
}

// Append adds an entry to the tape, assigns it an ID, and writes it as a JSON line.
func (s *JSONLStore) Append(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.ID = s.nextID
	s.nextID++

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	//nolint:gosec // tape file is not sensitive; path is controlled by the application
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}
