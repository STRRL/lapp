package tape

import (
	"bufio"
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
// It scans any existing file to resume IDs after the current maximum.
func NewJSONLStore(path string) *JSONLStore {
	nextID := scanMaxID(path)
	return &JSONLStore{
		path:   path,
		nextID: nextID,
	}
}

// scanMaxID reads an existing JSONL file and returns the next ID to use.
// Returns 1 if the file does not exist or contains no entries.
func scanMaxID(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()

	var maxID int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var row struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &row) == nil && row.ID > maxID {
			maxID = row.ID
		}
	}
	if maxID == 0 {
		return 1
	}
	return maxID + 1
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
