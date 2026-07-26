package webapp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/go-errors/errors"
	"github.com/google/uuid"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/workspace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultImportLimit = 100000
const recentImportQueryLimit = 20

func (s *WorkspaceService) CreateImportRun(_ context.Context, req *connect.Request[webv1.CreateImportRunRequest]) (*connect.Response[webv1.CreateImportRunResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	from, to, limit, connectErr := validateImportRunRequest(req.Msg)
	if connectErr != nil {
		return nil, connectErr
	}
	if s.importFetcher == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("import fetcher is not configured"))
	}

	if !s.reserveRun(id) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("another run is active"))
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		s.releaseRun(id)
		return nil, connectErr
	}
	runID, err := uuid.NewV7()
	if err != nil {
		s.releaseRun(id)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	record := workspace.ImportRunRecord{
		ID:        runID.String(),
		Provider:  "gcp",
		Project:   req.Msg.Project,
		Filter:    req.Msg.Filter,
		From:      from,
		To:        to,
		Limit:     limit,
		State:     workspace.ImportRunStateQueued,
		StartedAt: time.Now().UTC(),
	}
	if err := workspace.WriteImportRunRecord(s.workspaceDir(id), record); err != nil {
		s.releaseRun(id)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	message, err := s.importRunMessage(id, record)
	if err != nil {
		s.releaseRun(id)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	go func() {
		defer s.releaseRun(id)
		_, err := workspace.RunImport(context.Background(), workspace.ImportConfig{
			Dir:      s.workspaceDir(id),
			RunID:    record.ID,
			Provider: record.Provider,
			Project:  record.Project,
			Filter:   record.Filter,
			From:     record.From,
			To:       record.To,
			Limit:    record.Limit,
			Fetcher:  s.importFetcher,
		})
		if err != nil {
			slog.Error("ImportRun failed", "workspace", id, "run", record.ID, "error", err)
			return
		}
	}()

	return connect.NewResponse(&webv1.CreateImportRunResponse{
		ImportRun: message,
	}), nil
}

func validateImportRunRequest(req *webv1.CreateImportRunRequest) (from, to time.Time, limit int, connectErr *connect.Error) {
	if strings.TrimSpace(req.Project) == "" {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("project must not be blank"))
	}
	if req.From == nil || req.To == nil {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("from and to are required"))
	}
	if err := req.From.CheckValid(); err != nil {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid from timestamp: %w", err))
	}
	if err := req.To.CheckValid(); err != nil {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid to timestamp: %w", err))
	}
	from = req.From.AsTime().UTC()
	to = req.To.AsTime().UTC()
	if from.After(to) {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("from must not be after to"))
	}
	if req.Limit < 0 {
		return time.Time{}, time.Time{}, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("limit must not be negative"))
	}
	limit = int(req.Limit)
	if limit == 0 {
		limit = defaultImportLimit
	}
	return from, to, limit, nil
}

func (s *WorkspaceService) GetImportRun(_ context.Context, req *connect.Request[webv1.GetImportRunRequest]) (*connect.Response[webv1.GetImportRunResponse], error) {
	id, runID, connectErr := importRunNameFromResource(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadImportRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	message, err := s.importRunMessage(id, record)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.GetImportRunResponse{
		ImportRun: message,
	}), nil
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
		if records[i].StartedAt.Equal(records[j].StartedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].StartedAt.After(records[j].StartedAt)
	})
	runs := make([]*webv1.ImportRun, 0, len(records))
	for _, record := range records {
		message, err := s.importRunMessage(id, record)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		runs = append(runs, message)
	}
	return connect.NewResponse(&webv1.ListImportRunsResponse{ImportRuns: runs}), nil
}

func (s *WorkspaceService) ListRecentImportQueries(_ context.Context, _ *connect.Request[webv1.ListRecentImportQueriesRequest]) (*connect.Response[webv1.ListRecentImportQueriesResponse], error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&webv1.ListRecentImportQueriesResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	type queryKey struct {
		project string
		filter  string
	}
	latest := make(map[queryKey]time.Time)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		records, err := workspace.ListImportRunRecords(s.workspaceDir(entry.Name()))
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		for _, record := range records {
			key := queryKey{project: record.Project, filter: record.Filter}
			if usedAt, ok := latest[key]; !ok || record.StartedAt.After(usedAt) {
				latest[key] = record.StartedAt
			}
		}
	}

	queries := make([]*webv1.RecentImportQuery, 0, len(latest))
	for key, usedAt := range latest {
		queries = append(queries, &webv1.RecentImportQuery{
			Project:    key.project,
			Filter:     key.filter,
			LastUsedAt: timestamppb.New(usedAt),
		})
	}
	sort.Slice(queries, func(i, j int) bool {
		leftTime := queries[i].LastUsedAt.AsTime()
		rightTime := queries[j].LastUsedAt.AsTime()
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if queries[i].Project != queries[j].Project {
			return queries[i].Project < queries[j].Project
		}
		return queries[i].Filter < queries[j].Filter
	})
	if len(queries) > recentImportQueryLimit {
		queries = queries[:recentImportQueryLimit]
	}
	return connect.NewResponse(&webv1.ListRecentImportQueriesResponse{RecentQueries: queries}), nil
}

func (s *WorkspaceService) importRunMessage(workspaceID string, record workspace.ImportRunRecord) (*webv1.ImportRun, error) {
	limit, err := importRunInt32("limit", record.Limit)
	if err != nil {
		return nil, err
	}
	entryCount, err := importRunInt32("entry count", record.EntryCount)
	if err != nil {
		return nil, err
	}
	msg := &webv1.ImportRun{
		Name:        fmt.Sprintf("%s/importRuns/%s", workspaceName(workspaceID), record.ID),
		ImportRunId: record.ID,
		Provider:    record.Provider,
		Project:     record.Project,
		Filter:      record.Filter,
		From:        timestamppb.New(record.From),
		To:          timestamppb.New(record.To),
		Limit:       limit,
		State:       importRunState(record.State),
		EntryCount:  entryCount,
		Truncated:   record.Truncated,
		LogFileName: record.LogFileName,
		StartedAt:   timestamppb.New(record.StartedAt),
	}
	if record.FinishedAt != nil {
		msg.FinishedAt = timestamppb.New(*record.FinishedAt)
	}
	if record.Error != nil {
		msg.Error = &webv1.ImportRunError{
			Code:    record.Error.Code,
			Message: record.Error.Message,
		}
	}
	return msg, nil
}

func importRunInt32(field string, value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, errors.Errorf("import run %s %d is outside int32 range", field, value)
	}
	return int32(value), nil
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
			if record.State != workspace.ImportRunStateQueued && record.State != workspace.ImportRunStateRunning {
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

func importRunNameFromResource(name string) (workspaceID, runID string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 4 && parts[0] == "workspaces" && parts[2] == "importRuns" && validResourcePart(parts[1]) && validResourcePart(parts[3]) {
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
