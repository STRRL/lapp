package tape

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONLAppendSequentialID(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)

	s1, err := OpenJSONL(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Append(Message(map[string]any{"role": "user", "content": "hi"}, map[string]any{"run_id": "a"})); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := OpenJSONL(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Append(System("sys", map[string]any{"run_id": "b"})); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", data)
}
