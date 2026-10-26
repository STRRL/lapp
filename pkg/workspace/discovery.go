package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/go-errors/errors"
)

const DiscoveryRunsDirName = "discovery-runs"

// DiscoveryRunFormat versions how a run's templates were mined. Runs of
// another format are not read: their templates cannot match lines extracted
// today, so the log has to be imported again. Format 2 keeps the NDJSON
// level out of templates; runs before it have no format.
const DiscoveryRunFormat = 2

// ErrOldDiscoveryRun is returned for a run of another DiscoveryRunFormat.
var ErrOldDiscoveryRun = errors.New("made by an older lapp; import the log again")

type DiscoveryRunState string

const (
	DiscoveryRunStateQueued    DiscoveryRunState = "QUEUED"
	DiscoveryRunStateRunning   DiscoveryRunState = "RUNNING"
	DiscoveryRunStateSucceeded DiscoveryRunState = "SUCCEEDED"
	DiscoveryRunStateFailed    DiscoveryRunState = "FAILED"
)

type DiscoveryStep string

const (
	DiscoveryStepReadingLogs         DiscoveryStep = "READING_LOGS"
	DiscoveryStepMergingEntries      DiscoveryStep = "MERGING_ENTRIES"
	DiscoveryStepDiscoveringPatterns DiscoveryStep = "DISCOVERING_PATTERNS"
	DiscoveryStepLabelingPatterns    DiscoveryStep = "LABELING_PATTERNS"
	DiscoveryStepWritingResults      DiscoveryStep = "WRITING_RESULTS"
)

type DiscoveryRunRecord struct {
	ID              string                `json:"id"`
	Format          int                   `json:"format"`
	State           DiscoveryRunState     `json:"state"`
	CurrentStep     DiscoveryStep         `json:"current_step,omitempty"`
	ProgressMessage string                `json:"progress_message,omitempty"`
	ErrorMessage    string                `json:"error_message,omitempty"`
	Progress        *DiscoveryRunProgress `json:"progress,omitempty"`
	Error           *DiscoveryRunError    `json:"error,omitempty"`
	StartedAt       time.Time             `json:"started_at"`
	FinishedAt      *time.Time            `json:"finished_at,omitempty"`
	LogFileCount    int                   `json:"log_file_count"`
	LineCount       int                   `json:"line_count"`
	SplitMode       SplitMode             `json:"split_mode,omitempty"`
	// PhysicalLineCount counts the lines read from logs/; LineCount counts
	// the entries they were split into.
	PhysicalLineCount int `json:"physical_line_count,omitempty"`
	// CappedEntryCount counts entries cut at the maximum entry size.
	CappedEntryCount int `json:"capped_entry_count,omitempty"`
	PatternCount     int `json:"pattern_count"`
	UnmatchedCount   int `json:"unmatched_count"`
	// TruncatedLineCount counts lines cut at logsource.MaxLineBytes.
	TruncatedLineCount int           `json:"truncated_line_count,omitempty"`
	Patterns           []PatternInfo `json:"patterns,omitempty"`
	Unmatched          []TaggedLine  `json:"unmatched,omitempty"`
}

// SplitMode records how plain text files were split into entries. Empty
// means every file was structured (one record per entry).
type SplitMode string

const (
	// SplitModeMerged marks a run where timestamps at line starts marked
	// the entries and continuation lines joined the entry before them.
	SplitModeMerged SplitMode = "MERGED"
	// SplitModePerLine marks a run where at least one plain text file had
	// too few timestamps at line starts, so each line became its own entry.
	SplitModePerLine SplitMode = "PER_LINE"
)

type DiscoveryRunProgress struct {
	Step       DiscoveryStep                `json:"step,omitempty"`
	LabelBatch *DiscoveryLabelBatchProgress `json:"label_batch,omitempty"`
}

type DiscoveryLabelBatchProgress struct {
	Event          string `json:"event,omitempty"`
	BatchNumber    int    `json:"batch_number"`
	BatchCount     int    `json:"batch_count"`
	BatchSize      int    `json:"batch_size"`
	Attempt        int    `json:"attempt"`
	MaxAttempts    int    `json:"max_attempts"`
	CompletedCount int    `json:"completed_count"`
}

type DiscoveryRunError struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Step    DiscoveryStep `json:"step,omitempty"`
}

func DiscoveryRunsDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, DiscoveryRunsDirName)
}

func DiscoveryRunDir(workspaceDir, runID string) string {
	return filepath.Join(DiscoveryRunsDir(workspaceDir), runID)
}

func DiscoveryRunRecordPath(workspaceDir, runID string) string {
	return filepath.Join(DiscoveryRunDir(workspaceDir, runID), "run.json")
}

func WriteDiscoveryRunRecord(workspaceDir string, record DiscoveryRunRecord) error {
	if record.ID == "" {
		return errors.New("discovery run id is required")
	}
	record.Format = DiscoveryRunFormat
	runDir := DiscoveryRunDir(workspaceDir, record.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return errors.Errorf("create discovery run dir: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return errors.Errorf("marshal discovery run: %w", err)
	}
	tmp, err := os.CreateTemp(runDir, "run-*.json.tmp")
	if err != nil {
		return errors.Errorf("create temp discovery run: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return errors.Errorf("write temp discovery run: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return errors.Errorf("close temp discovery run: %w", err)
	}
	if err := os.Rename(tmpPath, DiscoveryRunRecordPath(workspaceDir, record.ID)); err != nil {
		return errors.Errorf("write discovery run: %w", err)
	}
	return nil
}

func ReadDiscoveryRunRecord(workspaceDir, runID string) (DiscoveryRunRecord, error) {
	data, err := os.ReadFile(DiscoveryRunRecordPath(workspaceDir, runID))
	if err != nil {
		return DiscoveryRunRecord{}, errors.Errorf("read discovery run: %w", err)
	}
	var record DiscoveryRunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return DiscoveryRunRecord{}, errors.Errorf("decode discovery run: %w", err)
	}
	if record.Format != DiscoveryRunFormat {
		return DiscoveryRunRecord{}, errors.Errorf("discovery run %s: %w", runID, ErrOldDiscoveryRun)
	}
	return record, nil
}

func ListDiscoveryRunRecords(workspaceDir string) ([]DiscoveryRunRecord, error) {
	entries, err := os.ReadDir(DiscoveryRunsDir(workspaceDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Errorf("read discovery runs dir: %w", err)
	}
	records := make([]DiscoveryRunRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := ReadDiscoveryRunRecord(workspaceDir, entry.Name())
		if errors.Is(err, ErrOldDiscoveryRun) {
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func LatestDiscoveryRun(records []DiscoveryRunRecord) *DiscoveryRunRecord {
	var latest *DiscoveryRunRecord
	for i := range records {
		record := &records[i]
		if latest == nil || record.StartedAt.After(latest.StartedAt) {
			latest = record
		}
	}
	return latest
}

func LatestSuccessfulDiscoveryRun(records []DiscoveryRunRecord) *DiscoveryRunRecord {
	var latest *DiscoveryRunRecord
	for i := range records {
		record := &records[i]
		if record.State != DiscoveryRunStateSucceeded {
			continue
		}
		if latest == nil || record.StartedAt.After(latest.StartedAt) {
			latest = record
		}
	}
	return latest
}
