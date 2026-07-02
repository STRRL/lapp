package webapp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/go-errors/errors"
	"github.com/google/uuid"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/gcplog"
	"github.com/strrl/lapp/pkg/workspace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultImportLimit = 10000
const maxImportLimit = 100000
const defaultImportWindow = time.Hour

func (s *WorkspaceService) ImportLogs(_ context.Context, req *connect.Request[webv1.ImportLogsRequest]) (*connect.Response[webv1.ImportLogsResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	gcp := req.Msg.GetGcpLogging()
	if gcp == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("gcp_logging source is required"))
	}
	if strings.TrimSpace(gcp.ProjectId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("project_id is required"))
	}
	limit := int(gcp.Limit)
	if limit <= 0 {
		limit = defaultImportLimit
	}
	if limit > maxImportLimit {
		limit = maxImportLimit
	}
	since := time.Now().Add(-defaultImportWindow).UTC()
	if gcp.StartTime != nil {
		since = gcp.StartTime.AsTime()
	}
	var until time.Time
	if gcp.EndTime != nil {
		until = gcp.EndTime.AsTime()
	}
	if !until.IsZero() && until.Before(since) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("end_time must not be before start_time"))
	}
	if !s.reserveDiscovery(id) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("another job is already running for this workspace"))
	}
	runID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		s.clearRunning(id)
		return nil, connect.NewError(connect.CodeInternal, uuidErr)
	}
	record := workspace.ImportRunRecord{
		ID:         runID.String(),
		State:      workspace.ImportRunStateRunning,
		Source:     workspace.ImportSourceGCPLogging,
		GCPProject: gcp.ProjectId,
		Filter:     gcp.Filter,
		StartedAt:  time.Now().UTC(),
	}
	if err := workspace.WriteImportRunRecord(s.workspaceDir(id), record); err != nil {
		s.clearRunning(id)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	cfg := gcplog.FetchConfig{
		ProjectID: gcp.ProjectId,
		Filter:    gcp.Filter,
		Since:     since,
		Until:     until,
		Limit:     limit,
	}
	go s.runGcpImport(id, record, cfg)
	return connect.NewResponse(&webv1.ImportLogsResponse{ImportRun: s.importRunMessage(id, record)}), nil
}

func (s *WorkspaceService) GetImportRun(_ context.Context, req *connect.Request[webv1.GetImportRunRequest]) (*connect.Response[webv1.GetImportRunResponse], error) {
	id, runID, connectErr := importRunNameFromResource(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadImportRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&webv1.GetImportRunResponse{ImportRun: s.importRunMessage(id, record)}), nil
}

func (s *WorkspaceService) ListImportRuns(_ context.Context, req *connect.Request[webv1.ListImportRunsRequest]) (*connect.Response[webv1.ListImportRunsResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	records, err := workspace.ListImportRunRecords(s.workspaceDir(id))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt.After(records[j].StartedAt)
	})
	runs := make([]*webv1.ImportRun, 0, len(records))
	for _, record := range records {
		runs = append(runs, s.importRunMessage(id, record))
	}
	return connect.NewResponse(&webv1.ListImportRunsResponse{ImportRuns: runs}), nil
}

// runGcpImport fetches entries into the import run directory, then moves the
// finished file into the workspace logs directory.
func (s *WorkspaceService) runGcpImport(id string, record workspace.ImportRunRecord, cfg gcplog.FetchConfig) {
	defer s.clearRunning(id)
	workspaceDir := s.workspaceDir(id)
	slog.Info("ImportRun started", "workspace", id, "run", record.ID, "project", cfg.ProjectID)

	fail := func(code, message string) {
		finishedAt := time.Now().UTC()
		record.State = workspace.ImportRunStateFailed
		record.FinishedAt = &finishedAt
		record.Error = &workspace.ImportRunError{Code: code, Message: message}
		if err := workspace.WriteImportRunRecord(workspaceDir, record); err != nil {
			slog.Error("write import run record", "workspace", id, "run", record.ID, "error", err)
		}
		slog.Error("ImportRun failed", "workspace", id, "run", record.ID, "code", code, "message", message)
	}

	fetchPath := filepath.Join(workspace.ImportRunDir(workspaceDir, record.ID), "fetch.log")
	file, err := os.Create(fetchPath)
	if err != nil {
		fail("IMPORT_WRITE_FAILED", err.Error())
		return
	}

	cfg.OnProgress = func(fetched int) {
		record.Progress = &workspace.ImportRunProgress{FetchedCount: fetched}
		if err := workspace.WriteImportRunRecord(workspaceDir, record); err != nil {
			slog.Error("write import run progress", "workspace", id, "run", record.ID, "error", err)
		}
	}

	count, fetchErr := s.gcpFetcher.Fetch(context.Background(), cfg, file)
	closeErr := file.Close()
	if fetchErr != nil {
		fail("GCP_FETCH_FAILED", fetchErr.Error())
		return
	}
	if closeErr != nil {
		fail("IMPORT_WRITE_FAILED", closeErr.Error())
		return
	}
	if count == 0 {
		fail("IMPORT_NO_ENTRIES", "no log entries matched the filter and time range")
		return
	}

	fileName := importLogFileName(workspaceDir, cfg.ProjectID, record.StartedAt, record.ID)
	if err := os.Rename(fetchPath, filepath.Join(workspaceDir, "logs", fileName)); err != nil {
		fail("IMPORT_WRITE_FAILED", err.Error())
		return
	}

	finishedAt := time.Now().UTC()
	record.State = workspace.ImportRunStateSucceeded
	record.FinishedAt = &finishedAt
	record.EntryCount = count
	record.Progress = &workspace.ImportRunProgress{FetchedCount: count}
	record.LogFileName = fileName
	if err := workspace.WriteImportRunRecord(workspaceDir, record); err != nil {
		slog.Error("write import run record", "workspace", id, "run", record.ID, "error", err)
		return
	}
	slog.Info("ImportRun completed", "workspace", id, "run", record.ID, "entries", count, "file", fileName)
}

// importLogFileName picks a readable, collision free file name for the
// imported log inside the workspace logs directory.
func importLogFileName(workspaceDir, projectID string, startedAt time.Time, runID string) string {
	base := fmt.Sprintf("gcp-%s-%s", projectID, startedAt.UTC().Format("20060102-150405"))
	name := base + ".log"
	if _, err := os.Stat(filepath.Join(workspaceDir, "logs", name)); os.IsNotExist(err) {
		return name
	}
	suffix := runID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return base + "-" + suffix + ".log"
}

func (s *WorkspaceService) importRunMessage(workspaceID string, record workspace.ImportRunRecord) *webv1.ImportRun {
	msg := &webv1.ImportRun{
		Name:        fmt.Sprintf("%s/importRuns/%s", workspaceName(workspaceID), record.ID),
		ImportRunId: record.ID,
		State:       importRunState(record.State),
		Source:      record.Source,
		StartedAt:   timestamppb.New(record.StartedAt),
		EntryCount:  int32(record.EntryCount),
		LogFileName: record.LogFileName,
	}
	if record.Progress != nil {
		msg.Progress = &webv1.ImportRunProgress{FetchedCount: int32(record.Progress.FetchedCount)}
	}
	if record.Error != nil {
		msg.Error = &webv1.ImportRunError{Code: record.Error.Code, Message: record.Error.Message}
	}
	if record.FinishedAt != nil {
		msg.FinishedAt = timestamppb.New(*record.FinishedAt)
	}
	return msg
}

func importRunNameFromResource(name string) (workspaceID, runID string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 4 && parts[0] == "workspaces" && parts[2] == "importRuns" && parts[1] != "" && parts[3] != "" {
		return parts[1], parts[3], nil
	}
	return "", "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid import run name %q", name))
}

func importRunState(state workspace.ImportRunState) webv1.ImportRunState {
	switch state {
	case workspace.ImportRunStateQueued:
		return webv1.ImportRunState_IMPORT_RUN_STATE_QUEUED
	case workspace.ImportRunStateRunning:
		return webv1.ImportRunState_IMPORT_RUN_STATE_RUNNING
	case workspace.ImportRunStateSucceeded:
		return webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED
	case workspace.ImportRunStateFailed:
		return webv1.ImportRunState_IMPORT_RUN_STATE_FAILED
	default:
		return webv1.ImportRunState_IMPORT_RUN_STATE_UNSPECIFIED
	}
}

func (s *WorkspaceService) recoverInterruptedImportRuns() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Errorf("read workspace root: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		workspaceDir := s.workspaceDir(entry.Name())
		records, err := workspace.ListImportRunRecords(workspaceDir)
		if err != nil {
			return errors.Errorf("list import runs for %s: %w", entry.Name(), err)
		}
		for _, record := range records {
			if !isInterruptedImportRun(record.State) {
				continue
			}
			finishedAt := time.Now().UTC()
			record.State = workspace.ImportRunStateFailed
			record.FinishedAt = &finishedAt
			record.Error = &workspace.ImportRunError{
				Code:    "IMPORT_INTERRUPTED",
				Message: "Import did not complete before the web server stopped",
			}
			if err := workspace.WriteImportRunRecord(workspaceDir, record); err != nil {
				return errors.Errorf("mark import run %s interrupted: %w", record.ID, err)
			}
		}
	}
	return nil
}

func isInterruptedImportRun(state workspace.ImportRunState) bool {
	return state == workspace.ImportRunStateQueued || state == workspace.ImportRunStateRunning
}
