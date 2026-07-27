package workspace

import (
	"bufio"
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	goerrors "github.com/go-errors/errors"
	"github.com/google/uuid"
)

var errImportLimitReached = stderrors.New("import limit reached")

// ImportRequest is what the fetcher needs to pull entries from a provider.
type ImportRequest struct {
	Project string
	Filter  string
	From    time.Time
	To      time.Time
	Limit   int
}

// ImportFetchResult reports facts about one completed provider fetch.
type ImportFetchResult struct {
	Truncated bool
}

// ImportLineWriter writes one envelope NDJSON line without a trailing newline.
type ImportLineWriter func(line string) error

// ImportFetcher pulls matching entries from a provider. It is the narrow
// seam between run orchestration and provider network access, so tests can
// inject a fake without touching the network (precedent: DiscoveryConfig.Labeler).
type ImportFetcher func(
	ctx context.Context,
	req ImportRequest,
	writeLine ImportLineWriter,
) (ImportFetchResult, error)

type ImportConfig struct {
	Dir      string
	RunID    string
	Provider string
	Project  string
	Filter   string
	From     time.Time
	To       time.Time
	Limit    int
	Fetcher  ImportFetcher
}

// ImportResult summarizes a completed import run.
type ImportResult struct {
	RunID       string
	EntryCount  int
	Truncated   bool
	LogFileName string
}

// RunImport starts an ImportRun: it records provenance under
// import-runs/<run-id>/record.json, pulls entries through the fetcher, and
// lands them as one NDJSON log file. It never starts discovery (ADR 0005).
// Zero matching entries is a success with no log file written.
func RunImport(ctx context.Context, config ImportConfig) (ImportResult, error) {
	if config.Dir == "" {
		return ImportResult{}, goerrors.New("workspace dir is required")
	}
	if config.Provider == "" {
		return ImportResult{}, goerrors.New("import provider is required")
	}
	if config.Fetcher == nil {
		return ImportResult{}, goerrors.New("import fetcher is required")
	}
	runID, err := importRunID(config.RunID)
	if err != nil {
		return ImportResult{}, err
	}
	record, err := claimImportRun(config, runID)
	if err != nil {
		return ImportResult{}, err
	}
	slog.Info("ImportRun started", "run", runID, "provider", config.Provider, "project", config.Project)
	if err := WriteImportRunRecord(config.Dir, record); err != nil {
		return ImportResult{}, err
	}

	fetched, errorCode, err := fetchImport(ctx, config, record)
	if err != nil {
		failImportRun(config.Dir, &record, errorCode, err)
		return ImportResult{}, err
	}

	finishedAt := timeNow()
	record.State = ImportRunStateSucceeded
	record.FinishedAt = &finishedAt
	record.EntryCount = fetched.EntryCount
	record.Truncated = fetched.Truncated
	record.LogFileName = fetched.LogFileName
	if err := WriteImportRunRecord(config.Dir, record); err != nil {
		// Best effort: do not leave the run stuck in RUNNING when the final
		// record write fails. A secondary failure here is ignored.
		failImportRun(config.Dir, &record, "WRITE_RECORD_FAILED", err)
		return ImportResult{}, err
	}

	slog.Info(
		"ImportRun succeeded",
		"run", runID,
		"entries", record.EntryCount,
		"truncated", record.Truncated,
		"file", record.LogFileName,
	)
	return ImportResult{
		RunID:       runID,
		EntryCount:  record.EntryCount,
		Truncated:   record.Truncated,
		LogFileName: record.LogFileName,
	}, nil
}

type importFetchOutcome struct {
	EntryCount  int
	Truncated   bool
	LogFileName string
}

type importLogOutput struct {
	file         *os.File
	writer       *bufio.Writer
	tempPath     string
	logPath      string
	fileName     string
	limit        int
	entryCount   int
	limitReached bool
	writeErr     error
	closed       bool
	committed    bool
}

func fetchImport(
	ctx context.Context,
	config ImportConfig,
	record ImportRunRecord,
) (importFetchOutcome, string, error) {
	output, err := newImportLogOutput(config, record.ID)
	if err != nil {
		return importFetchOutcome{}, "WRITE_LOG_FAILED", err
	}
	defer output.cleanup()

	fetched, err := config.Fetcher(ctx, ImportRequest{
		Project: config.Project,
		Filter:  config.Filter,
		From:    record.From,
		To:      record.To,
		Limit:   config.Limit,
	}, output.writeLine)
	if err != nil && !stderrors.Is(err, errImportLimitReached) {
		if output.writeErr != nil {
			return importFetchOutcome{}, "WRITE_LOG_FAILED", output.writeErr
		}
		return importFetchOutcome{}, "FETCH_FAILED", err
	}
	if err := output.commit(); err != nil {
		return importFetchOutcome{}, "WRITE_LOG_FAILED", err
	}
	return importFetchOutcome{
		EntryCount:  output.entryCount,
		Truncated:   fetched.Truncated || output.limitReached,
		LogFileName: output.committedFileName(),
	}, "", nil
}

func newImportLogOutput(config ImportConfig, runID string) (*importLogOutput, error) {
	fileName := config.Provider + "-" + runID + ".ndjson"
	logPath := filepath.Join(config.Dir, "logs", fileName)
	file, err := os.CreateTemp(filepath.Dir(logPath), "."+fileName+".*.tmp")
	if err != nil {
		return nil, goerrors.Errorf("create imported log file: %w", err)
	}
	return &importLogOutput{
		file:     file,
		writer:   bufio.NewWriter(file),
		tempPath: file.Name(),
		logPath:  logPath,
		fileName: fileName,
		limit:    config.Limit,
	}, nil
}

func (output *importLogOutput) writeLine(line string) error {
	if output.limit > 0 && output.entryCount >= output.limit {
		output.limitReached = true
		return errImportLimitReached
	}
	if _, err := output.writer.WriteString(line); err != nil {
		output.writeErr = goerrors.Errorf("write imported log file: %w", err)
		return output.writeErr
	}
	if err := output.writer.WriteByte('\n'); err != nil {
		output.writeErr = goerrors.Errorf("write imported log file: %w", err)
		return output.writeErr
	}
	output.entryCount++
	return nil
}

func (output *importLogOutput) commit() error {
	if err := output.writer.Flush(); err != nil {
		return goerrors.Errorf("write imported log file: %w", err)
	}
	if err := output.file.Chmod(0o644); err != nil {
		return goerrors.Errorf("set imported log file permissions: %w", err)
	}
	if err := output.file.Close(); err != nil {
		return goerrors.Errorf("close imported log file: %w", err)
	}
	output.closed = true
	if output.entryCount == 0 {
		return nil
	}
	if err := os.Rename(output.tempPath, output.logPath); err != nil {
		return goerrors.Errorf("commit imported log file: %w", err)
	}
	output.committed = true
	return nil
}

func (output *importLogOutput) committedFileName() string {
	if !output.committed {
		return ""
	}
	return output.fileName
}

func (output *importLogOutput) cleanup() {
	if !output.closed {
		_ = output.file.Close()
	}
	if !output.committed {
		_ = os.Remove(output.tempPath)
	}
}

func claimImportRun(config ImportConfig, runID string) (ImportRunRecord, error) {
	record := ImportRunRecord{
		ID:        runID,
		Provider:  config.Provider,
		Project:   config.Project,
		Filter:    config.Filter,
		From:      config.From.UTC(),
		To:        config.To.UTC(),
		Limit:     config.Limit,
		State:     ImportRunStateRunning,
		StartedAt: timeNow(),
	}
	existing, err := ReadImportRunRecord(config.Dir, runID)
	if err != nil {
		if goerrors.Is(err, os.ErrNotExist) {
			return record, nil
		}
		return ImportRunRecord{}, goerrors.Errorf("check import run %q: %w", runID, err)
	}
	if existing.State != ImportRunStateQueued || !sameImportRequest(existing, record) {
		return ImportRunRecord{}, goerrors.Errorf("import run %q already exists", runID)
	}
	existing.State = ImportRunStateRunning
	return existing, nil
}

func sameImportRequest(left, right ImportRunRecord) bool {
	return left.ID == right.ID &&
		left.Provider == right.Provider &&
		left.Project == right.Project &&
		left.Filter == right.Filter &&
		left.From.Equal(right.From) &&
		left.To.Equal(right.To) &&
		left.Limit == right.Limit
}

func importRunID(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", goerrors.Errorf("create import run id: %w", err)
	}
	return id.String(), nil
}

func failImportRun(workspaceDir string, record *ImportRunRecord, code string, cause error) {
	finishedAt := timeNow()
	record.State = ImportRunStateFailed
	record.FinishedAt = &finishedAt
	record.Error = &ImportRunError{
		Code:    code,
		Message: cause.Error(),
	}
	slog.Error("ImportRun failed", "run", record.ID, "code", code, "error", cause)
	_ = WriteImportRunRecord(workspaceDir, *record)
}
