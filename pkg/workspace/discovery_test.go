package workspace

import (
	"errors"
	"os"
	"testing"
	"time"
)

// A run written before DiscoveryRunFormat has no format; it is not read.
func TestOldFormatDiscoveryRunIsNotRead(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDiscoveryRunRecord(dir, DiscoveryRunRecord{ID: "new", State: DiscoveryRunStateSucceeded, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(DiscoveryRunDir(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"id":"old","state":"SUCCEEDED","started_at":"2026-10-26T00:00:00Z","patterns":[{"template":"error x"}]}`
	if err := os.WriteFile(DiscoveryRunRecordPath(dir, "old"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadDiscoveryRunRecord(dir, "old"); !errors.Is(err, ErrOldDiscoveryRun) {
		t.Fatalf("read old run: err = %v, want ErrOldDiscoveryRun", err)
	}
	records, err := ListDiscoveryRunRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "new" || records[0].Format != DiscoveryRunFormat {
		t.Fatalf("records = %+v, want only the new run", records)
	}
}
