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
// It keeps the file handle open for the lifetime of the store.
type JSONLStore struct {
	mu     sync.Mutex
	file   *os.File
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

	if s.file == nil {
		//nolint:gosec // tape file path is controlled by the application
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		s.file = f
	}

	entry.ID = s.nextID
	s.nextID++

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	if _, err := s.file.Write(line); err != nil {
		return err
	}
	return s.file.Sync()
}

// Close closes the underlying file handle.
func (s *JSONLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// Path returns the file path of the store.
func (s *JSONLStore) Path() string {
	return s.path
}
