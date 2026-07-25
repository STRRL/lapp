package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-errors/errors"
)

func importTestConfig(dir string, fetcher ImportFetcher) ImportConfig {
	return ImportConfig{
		Dir:      dir,
		RunID:    "01900000-0000-7000-8000-00000000b001",
		Provider: "gcp",
		Project:  "acme-prod",
		Filter:   `severity>=ERROR`,
		From:     time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC),
		Limit:    100,
		Fetcher:  fetcher,
	}
}

func TestRunImportSuccessWritesFileAndRecord(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	lines := []string{
		`{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"a"}}`,
		`{"ts":"2026-07-25T00:00:02Z","severity":"ERROR","payload":{"message":"b"}}`,
	}
	var gotReq ImportRequest
	fetcher := func(_ context.Context, req ImportRequest) (ImportFetchResult, error) {
		gotReq = req
		return ImportFetchResult{Lines: lines}, nil
	}

	result, err := RunImport(context.Background(), importTestConfig(dir, fetcher))
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.EntryCount != 2 || result.Truncated {
		t.Fatalf("unexpected result: %+v", result)
	}
	wantFile := "gcp-01900000-0000-7000-8000-00000000b001.ndjson"
	if result.LogFileName != wantFile {
		t.Fatalf("log file name = %q, want %q", result.LogFileName, wantFile)
	}
	if gotReq.Project != "acme-prod" || gotReq.Filter != `severity>=ERROR` || gotReq.Limit != 100 {
		t.Fatalf("unexpected fetch request: %+v", gotReq)
	}

	content := mustRead(t, filepath.Join(dir, "logs", wantFile))
	if content != lines[0]+"\n"+lines[1]+"\n" {
		t.Fatalf("unexpected log file content:\n%s", content)
	}

	record, err := ReadImportRunRecord(dir, result.RunID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if record.State != ImportRunStateSucceeded {
		t.Fatalf("record state = %s, want SUCCEEDED", record.State)
	}
	if record.EntryCount != 2 || record.Truncated || record.LogFileName != wantFile {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Provider != "gcp" || record.Project != "acme-prod" || record.Limit != 100 {
		t.Fatalf("unexpected record provenance: %+v", record)
	}
	if record.FinishedAt == nil || record.Error != nil {
		t.Fatalf("expected finished record without error: %+v", record)
	}
}

func TestRunImportZeroEntriesSucceedsWithoutFile(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	fetcher := func(_ context.Context, _ ImportRequest) (ImportFetchResult, error) {
		return ImportFetchResult{}, nil
	}

	result, err := RunImport(context.Background(), importTestConfig(dir, fetcher))
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.EntryCount != 0 || result.LogFileName != "" {
		t.Fatalf("unexpected result: %+v", result)
	}

	logEntries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(logEntries) != 0 {
		t.Fatalf("expected no log files, found %d", len(logEntries))
	}

	record, err := ReadImportRunRecord(dir, result.RunID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if record.State != ImportRunStateSucceeded || record.EntryCount != 0 || record.LogFileName != "" {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func TestRunImportTruncationIsRecorded(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	fetcher := func(_ context.Context, _ ImportRequest) (ImportFetchResult, error) {
		return ImportFetchResult{
			Lines:     []string{`{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"a"}}`},
			Truncated: true,
		}, nil
	}

	result, err := RunImport(context.Background(), importTestConfig(dir, fetcher))
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncated result: %+v", result)
	}

	record, err := ReadImportRunRecord(dir, result.RunID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if !record.Truncated || record.State != ImportRunStateSucceeded {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func TestRunImportFetchFailureWritesFailedRecord(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	fetcher := func(_ context.Context, _ ImportRequest) (ImportFetchResult, error) {
		return ImportFetchResult{}, errors.New("could not find default credentials")
	}

	config := importTestConfig(dir, fetcher)
	if _, err := RunImport(context.Background(), config); err == nil {
		t.Fatal("expected fetch error")
	}

	record, err := ReadImportRunRecord(dir, config.RunID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if record.State != ImportRunStateFailed {
		t.Fatalf("record state = %s, want FAILED", record.State)
	}
	if record.Error == nil || record.Error.Code != "FETCH_FAILED" || record.Error.Message == "" {
		t.Fatalf("unexpected record error: %+v", record.Error)
	}
	if record.FinishedAt == nil {
		t.Fatal("expected finished timestamp on failed record")
	}

	logEntries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(logEntries) != 0 {
		t.Fatalf("expected no log files, found %d", len(logEntries))
	}
}

func TestRunImportCapsLinesAtLimit(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	fetcher := func(_ context.Context, _ ImportRequest) (ImportFetchResult, error) {
		return ImportFetchResult{
			Lines: []string{
				`{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"a"}}`,
				`{"ts":"2026-07-25T00:00:02Z","severity":"ERROR","payload":{"message":"b"}}`,
				`{"ts":"2026-07-25T00:00:03Z","severity":"ERROR","payload":{"message":"c"}}`,
			},
		}, nil
	}

	config := importTestConfig(dir, fetcher)
	config.Limit = 2
	result, err := RunImport(context.Background(), config)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.EntryCount != 2 || !result.Truncated {
		t.Fatalf("expected capped truncated result, got %+v", result)
	}

	content := mustRead(t, filepath.Join(dir, "logs", result.LogFileName))
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 lines written, got %d:\n%s", len(lines), content)
	}
}

func TestRunImportRejectsDuplicateRunID(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "logs"))
	fetcher := func(_ context.Context, _ ImportRequest) (ImportFetchResult, error) {
		return ImportFetchResult{
			Lines: []string{`{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"a"}}`},
		}, nil
	}

	config := importTestConfig(dir, fetcher)
	existing := ImportRunRecord{
		ID:        config.RunID,
		Provider:  "gcp",
		Project:   "acme-prod",
		State:     ImportRunStateSucceeded,
		StartedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
	}
	if err := WriteImportRunRecord(dir, existing); err != nil {
		t.Fatalf("WriteImportRunRecord: %v", err)
	}

	if _, err := RunImport(context.Background(), config); err == nil {
		t.Fatal("expected error for duplicate run id")
	}

	record, err := ReadImportRunRecord(dir, config.RunID)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if !reflect.DeepEqual(record, existing) {
		t.Fatalf("existing record was modified\ngot:  %+v\nwant: %+v", record, existing)
	}

	logEntries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(logEntries) != 0 {
		t.Fatalf("expected no log files, found %d", len(logEntries))
	}
}
