package workspace

import (
	"reflect"
	"testing"
	"time"
)

func TestImportRunRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	finishedAt := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	record := ImportRunRecord{
		ID:          "01900000-0000-7000-8000-00000000a001",
		Provider:    "gcp",
		Project:     "acme-prod",
		Filter:      `resource.type="cloud_run_revision"`,
		From:        time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC),
		Limit:       100000,
		State:       ImportRunStateSucceeded,
		EntryCount:  42,
		Truncated:   true,
		LogFileName: "gcp-01900000-0000-7000-8000-00000000a001.ndjson",
		StartedAt:   time.Date(2026, 7, 25, 1, 0, 5, 0, time.UTC),
		FinishedAt:  &finishedAt,
		Error: &ImportRunError{
			Code:    "FETCH_FAILED",
			Message: "boom",
		},
	}

	if err := WriteImportRunRecord(dir, record); err != nil {
		t.Fatalf("WriteImportRunRecord: %v", err)
	}
	got, err := ReadImportRunRecord(dir, record.ID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip mismatch\ngot:  %+v\nwant: %+v", got, record)
	}
}

func TestWriteImportRunRecordRequiresID(t *testing.T) {
	if err := WriteImportRunRecord(t.TempDir(), ImportRunRecord{}); err == nil {
		t.Fatal("expected error for missing run id")
	}
}

func TestListImportRunRecordsMissingDirIsEmpty(t *testing.T) {
	records, err := ListImportRunRecords(t.TempDir())
	if err != nil {
		t.Fatalf("ListImportRunRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no records, got %d", len(records))
	}
}
