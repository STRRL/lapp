package workspace

import (
	"testing"
	"time"
)

func TestImportRunRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	finishedAt := time.Date(2026, 7, 1, 10, 5, 0, 0, time.UTC)
	record := ImportRunRecord{
		ID:         "run-1",
		State:      ImportRunStateSucceeded,
		Source:     ImportSourceGCPLogging,
		GCPProject: "my-project",
		Filter:     "severity>=ERROR",
		Progress: &ImportRunProgress{
			FetchedCount: 1200,
		},
		StartedAt:   time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		FinishedAt:  &finishedAt,
		EntryCount:  1200,
		LogFileName: "gcp-my-project-1.log",
	}

	if err := WriteImportRunRecord(dir, record); err != nil {
		t.Fatal(err)
	}
	got, err := ReadImportRunRecord(dir, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != record.ID || got.State != record.State || got.Source != record.Source {
		t.Errorf("got %+v, want %+v", got, record)
	}
	if got.EntryCount != 1200 || got.LogFileName != record.LogFileName {
		t.Errorf("got %+v, want %+v", got, record)
	}
	if got.Progress == nil || got.Progress.FetchedCount != 1200 {
		t.Errorf("progress = %+v, want fetched 1200", got.Progress)
	}
}

func TestWriteImportRunRecordRequiresID(t *testing.T) {
	if err := WriteImportRunRecord(t.TempDir(), ImportRunRecord{}); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestListImportRunRecords(t *testing.T) {
	dir := t.TempDir()

	records, err := ListImportRunRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("records = %d, want 0", len(records))
	}

	for _, id := range []string{"run-1", "run-2"} {
		record := ImportRunRecord{
			ID:        id,
			State:     ImportRunStateRunning,
			Source:    ImportSourceGCPLogging,
			StartedAt: time.Now().UTC(),
		}
		if err := WriteImportRunRecord(dir, record); err != nil {
			t.Fatal(err)
		}
	}

	records, err = ListImportRunRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Errorf("records = %d, want 2", len(records))
	}
}
