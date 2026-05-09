package tape

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const FileName = ".tape.jsonl"

type JSONLStore struct {
	path   string
	file   *os.File
	mu     sync.Mutex
	nextID int64
}

func OpenJSONL(path string) (*JSONLStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	nextID, err := scanMaxID(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONLStore{path: path, file: f, nextID: nextID}, nil
}

func scanMaxID(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	defer f.Close()

	var maxID int64
	sc := bufio.NewScanner(f)
	// Allow entries up to 10 MB to handle large tool outputs in tape lines.
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var row struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &row) == nil && row.ID > maxID {
			maxID = row.ID
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if maxID == 0 {
		return 1, nil
	}
	return maxID + 1, nil
}

func (s *JSONLStore) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e.ID = s.nextID
	s.nextID++

	enc, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(append(enc, '\n')); err != nil {
		return err
	}
	return s.file.Sync()
}

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

func (s *JSONLStore) Path() string {
	return s.path
}
