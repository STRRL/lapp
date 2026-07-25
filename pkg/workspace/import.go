package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
)

// ImportRequest is what the fetcher needs to pull entries from a provider.
type ImportRequest struct {
	Project string
	Filter  string
	From    time.Time
	To      time.Time
	Limit   int
}

// ImportFetchResult carries provider entries already converted to envelope
// NDJSON lines. Truncated is true when the limit cut the result short.
type ImportFetchResult struct {
	Lines     []string
	Truncated bool
}

// ImportFetcher pulls matching entries from a provider. It is the narrow
// seam between run orchestration and provider network access, so tests can
// inject a fake without touching the network (precedent: DiscoveryConfig.Labeler).
type ImportFetcher func(ctx context.Context, req ImportRequest) (ImportFetchResult, error)

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
		return ImportResult{}, errors.New("workspace dir is required")
	}
	if config.Provider == "" {
		return ImportResult{}, errors.New("import provider is required")
	}
	if config.Fetcher == nil {
		return ImportResult{}, errors.New("import fetcher is required")
	}
	runID, err := importRunID(config.RunID)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err := os.Stat(ImportRunRecordPath(config.Dir, runID)); err == nil {
		return ImportResult{}, errors.Errorf("import run %q already exists", runID)
	} else if !os.IsNotExist(err) {
		return ImportResult{}, errors.Errorf("check import run %q: %w", runID, err)
	}

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
	slog.Info("ImportRun started", "run", runID, "provider", config.Provider, "project", config.Project)
	if err := WriteImportRunRecord(config.Dir, record); err != nil {
		return ImportResult{}, err
	}

	fetched, err := config.Fetcher(ctx, ImportRequest{
		Project: config.Project,
		Filter:  config.Filter,
		From:    record.From,
		To:      record.To,
		Limit:   config.Limit,
	})
	if err != nil {
		failImportRun(config.Dir, &record, "FETCH_FAILED", err)
		return ImportResult{}, err
	}
	// The limit is enforced here as well as in the fetcher so the cap holds
	// regardless of provider behavior.
	if config.Limit > 0 && len(fetched.Lines) > config.Limit {
		fetched.Lines = fetched.Lines[:config.Limit]
		fetched.Truncated = true
	}

	if len(fetched.Lines) > 0 {
		fileName := config.Provider + "-" + runID + ".ndjson"
		logPath := filepath.Join(config.Dir, "logs", fileName)
		content := strings.Join(fetched.Lines, "\n") + "\n"
		if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
			err = errors.Errorf("write imported log file: %w", err)
			failImportRun(config.Dir, &record, "WRITE_LOG_FAILED", err)
			return ImportResult{}, err
		}
		record.LogFileName = fileName
	}

	finishedAt := timeNow()
	record.State = ImportRunStateSucceeded
	record.FinishedAt = &finishedAt
	record.EntryCount = len(fetched.Lines)
	record.Truncated = fetched.Truncated
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

func importRunID(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", errors.Errorf("create import run id: %w", err)
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
