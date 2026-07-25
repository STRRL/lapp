package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/go-errors/errors"
)

const ImportRunsDirName = "import-runs"

type ImportRunState string

const (
	ImportRunStateQueued    ImportRunState = "QUEUED"
	ImportRunStateRunning   ImportRunState = "RUNNING"
	ImportRunStateSucceeded ImportRunState = "SUCCEEDED"
	ImportRunStateFailed    ImportRunState = "FAILED"
)

// ImportRunRecord is the persisted provenance for one provider import.
// These records are the only store of import history (ADR 0004). Provider
// metadata such as labels, trace, and resource belongs here, never in the
// imported log lines.
type ImportRunRecord struct {
	ID          string          `json:"id"`
	Provider    string          `json:"provider"`
	Project     string          `json:"project"`
	Filter      string          `json:"filter,omitempty"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Limit       int             `json:"limit"`
	State       ImportRunState  `json:"state"`
	EntryCount  int             `json:"entry_count"`
	Truncated   bool            `json:"truncated"`
	LogFileName string          `json:"log_file_name,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Error       *ImportRunError `json:"error,omitempty"`
}

type ImportRunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ImportRunsDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, ImportRunsDirName)
}

func ImportRunDir(workspaceDir, runID string) string {
	return filepath.Join(ImportRunsDir(workspaceDir), runID)
}

func ImportRunRecordPath(workspaceDir, runID string) string {
	return filepath.Join(ImportRunDir(workspaceDir, runID), "record.json")
}

func WriteImportRunRecord(workspaceDir string, record ImportRunRecord) error {
	if record.ID == "" {
		return errors.New("import run id is required")
	}
	runDir := ImportRunDir(workspaceDir, record.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return errors.Errorf("create import run dir: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return errors.Errorf("marshal import run: %w", err)
	}
	tmp, err := os.CreateTemp(runDir, "record-*.json.tmp")
	if err != nil {
		return errors.Errorf("create temp import run: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return errors.Errorf("write temp import run: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return errors.Errorf("close temp import run: %w", err)
	}
	if err := os.Rename(tmpPath, ImportRunRecordPath(workspaceDir, record.ID)); err != nil {
		return errors.Errorf("write import run: %w", err)
	}
	return nil
}

func ReadImportRunRecord(workspaceDir, runID string) (ImportRunRecord, error) {
	data, err := os.ReadFile(ImportRunRecordPath(workspaceDir, runID))
	if err != nil {
		return ImportRunRecord{}, errors.Errorf("read import run: %w", err)
	}
	var record ImportRunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ImportRunRecord{}, errors.Errorf("decode import run: %w", err)
	}
	return record, nil
}

func ListImportRunRecords(workspaceDir string) ([]ImportRunRecord, error) {
	entries, err := os.ReadDir(ImportRunsDir(workspaceDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Errorf("read import runs dir: %w", err)
	}
	records := make([]ImportRunRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := ReadImportRunRecord(workspaceDir, entry.Name())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
