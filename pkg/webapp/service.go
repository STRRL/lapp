package webapp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/go-errors/errors"
	"github.com/google/uuid"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/gen/go/lapp/web/v1/webv1connect"
	"github.com/strrl/lapp/pkg/workspace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ webv1connect.WorkspaceServiceHandler = (*WorkspaceService)(nil)

var workspaceIDPattern = regexp.MustCompile(`[^a-z0-9]+`)
var errorLikePattern = regexp.MustCompile(`(?i)(error|warn|fatal|panic|exception|failed|timeout)`)

type WorkspaceService struct {
	root    string
	apiKey  string
	model   string
	mu      sync.Mutex
	running map[string]bool
	labeler workspace.PatternLabeler
}

type ServiceConfig struct {
	Root    string
	APIKey  string
	Model   string
	Labeler workspace.PatternLabeler
}

func NewWorkspaceService(config ServiceConfig) (*WorkspaceService, error) {
	root := config.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.Errorf("resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".lapp", "workspaces")
	}
	service := &WorkspaceService{
		root:    root,
		apiKey:  config.APIKey,
		model:   config.Model,
		running: make(map[string]bool),
		labeler: config.Labeler,
	}
	if err := service.recoverInterruptedDiscoveryRuns(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *WorkspaceService) ListWorkspaces(ctx context.Context, _ *connect.Request[webv1.ListWorkspacesRequest]) (*connect.Response[webv1.ListWorkspacesResponse], error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&webv1.ListWorkspacesResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	workspaces := make([]*webv1.Workspace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ws, err := s.workspaceMessage(ctx, entry.Name())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		workspaces = append(workspaces, ws)
	}
	sort.Slice(workspaces, func(i, j int) bool {
		return workspaces[i].WorkspaceId < workspaces[j].WorkspaceId
	})
	return connect.NewResponse(&webv1.ListWorkspacesResponse{Workspaces: workspaces}), nil
}

func (s *WorkspaceService) GetWorkspace(ctx context.Context, req *connect.Request[webv1.GetWorkspaceRequest]) (*connect.Response[webv1.GetWorkspaceResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	ws, err := s.workspaceMessage(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&webv1.GetWorkspaceResponse{Workspace: ws}), nil
}

func (s *WorkspaceService) CreateWorkspace(ctx context.Context, req *connect.Request[webv1.CreateWorkspaceRequest]) (*connect.Response[webv1.CreateWorkspaceResponse], error) {
	id := sanitizeWorkspaceID(req.Msg.WorkspaceId)
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	dir := s.workspaceDir(id)
	if _, err := os.Stat(dir); err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.Errorf("workspace %q already exists", id))
	} else if err != nil && !os.IsNotExist(err) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	for _, sub := range []string{"logs", workspace.DiscoveryRunsDirName} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	ws, err := s.workspaceMessage(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.CreateWorkspaceResponse{Workspace: ws}), nil
}

func (s *WorkspaceService) DeleteWorkspace(_ context.Context, req *connect.Request[webv1.DeleteWorkspaceRequest]) (*connect.Response[webv1.DeleteWorkspaceResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	s.mu.Lock()
	running := s.running[id]
	s.mu.Unlock()
	if running {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("discovery is running"))
	}
	if err := os.RemoveAll(s.workspaceDir(id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.DeleteWorkspaceResponse{}), nil
}

func (s *WorkspaceService) ListLogFiles(_ context.Context, req *connect.Request[webv1.ListLogFilesRequest]) (*connect.Response[webv1.ListLogFilesResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	files, err := s.listLogFileMessages(id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.ListLogFilesResponse{LogFiles: files}), nil
}

func (s *WorkspaceService) UploadLogFile(_ context.Context, req *connect.Request[webv1.UploadLogFileRequest]) (*connect.Response[webv1.UploadLogFileResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	if s.isRunning(id) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("discovery is running"))
	}
	fileName := filepath.Base(req.Msg.FileName)
	if fileName == "." || fileName == string(filepath.Separator) || fileName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("file_name is required"))
	}
	path := filepath.Join(s.workspaceDir(id), "logs", fileName)
	if _, err := os.Stat(path); err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.Errorf("log file %q already exists", fileName))
	} else if err != nil && !os.IsNotExist(err) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := os.WriteFile(path, req.Msg.Content, 0o644); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	msg, err := s.logFileMessage(id, fileName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.UploadLogFileResponse{LogFile: msg}), nil
}

func (s *WorkspaceService) DeleteLogFile(_ context.Context, req *connect.Request[webv1.DeleteLogFileRequest]) (*connect.Response[webv1.DeleteLogFileResponse], error) {
	id, fileName, connectErr := logFileNameFromResource(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	if s.isRunning(id) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("discovery is running"))
	}
	if err := os.Remove(filepath.Join(s.workspaceDir(id), "logs", fileName)); err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&webv1.DeleteLogFileResponse{}), nil
}

func (s *WorkspaceService) CreateDiscoveryRun(_ context.Context, req *connect.Request[webv1.CreateDiscoveryRunRequest]) (*connect.Response[webv1.CreateDiscoveryRunResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	files, err := s.listLogFileMessages(id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if len(files) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("discovery requires at least one log file"))
	}
	if !s.reserveDiscovery(id) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("discovery is already running"))
	}
	runID, uuidErr := uuid.NewV7()
	if uuidErr != nil {
		s.setRunning(id, false)
		return nil, connect.NewError(connect.CodeInternal, uuidErr)
	}
	record := workspace.DiscoveryRunRecord{
		ID:          runID.String(),
		State:       workspace.DiscoveryRunStateRunning,
		CurrentStep: workspace.DiscoveryStepReadingLogs,
		Progress: &workspace.DiscoveryRunProgress{
			Step: workspace.DiscoveryStepReadingLogs,
		},
		StartedAt: time.Now().UTC(),
	}
	if err := workspace.WriteDiscoveryRunRecord(s.workspaceDir(id), record); err != nil {
		s.setRunning(id, false)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	go func() {
		defer s.setRunning(id, false)
		slog.Info("DiscoveryRun started", "workspace", id, "run", runID.String())
		result, err := workspace.Discover(context.Background(), workspace.DiscoveryConfig{
			Dir:     s.workspaceDir(id),
			RunID:   runID.String(),
			APIKey:  s.apiKey,
			Model:   firstNonEmpty(req.Msg.Model, s.model),
			Labeler: s.labeler,
		})
		if err != nil {
			slog.Error("DiscoveryRun failed", "workspace", id, "run", runID.String(), "error", err)
			return
		}
		slog.Info(
			"DiscoveryRun completed",
			"workspace", id,
			"run", runID.String(),
			"files", result.FileCount,
			"lines", result.LineCount,
			"patterns", result.PatternCount,
			"unmatched", result.UnmatchedCount,
		)
	}()
	return connect.NewResponse(&webv1.CreateDiscoveryRunResponse{DiscoveryRun: s.discoveryRunMessage(id, record)}), nil
}

func (s *WorkspaceService) GetDiscoveryRun(_ context.Context, req *connect.Request[webv1.GetDiscoveryRunRequest]) (*connect.Response[webv1.GetDiscoveryRunResponse], error) {
	id, runID, connectErr := discoveryRunNameFromResource(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadDiscoveryRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&webv1.GetDiscoveryRunResponse{DiscoveryRun: s.discoveryRunMessage(id, record)}), nil
}

func (s *WorkspaceService) ListDiscoveryRuns(_ context.Context, req *connect.Request[webv1.ListDiscoveryRunsRequest]) (*connect.Response[webv1.ListDiscoveryRunsResponse], error) {
	id, connectErr := workspaceIDFromName(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	if connectErr := s.ensureWorkspaceExists(id); connectErr != nil {
		return nil, connectErr
	}
	records, err := workspace.ListDiscoveryRunRecords(s.workspaceDir(id))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt.After(records[j].StartedAt)
	})
	runs := make([]*webv1.DiscoveryRun, 0, len(records))
	for _, record := range records {
		runs = append(runs, s.discoveryRunMessage(id, record))
	}
	return connect.NewResponse(&webv1.ListDiscoveryRunsResponse{DiscoveryRuns: runs}), nil
}

func (s *WorkspaceService) ListPatterns(_ context.Context, req *connect.Request[webv1.ListPatternsRequest]) (*connect.Response[webv1.ListPatternsResponse], error) {
	id, runID, connectErr := discoveryRunNameFromResource(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadDiscoveryRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	patterns := make([]*webv1.Pattern, 0, len(record.Patterns))
	for _, p := range record.Patterns {
		patterns = append(patterns, patternMessage(id, runID, p))
	}
	return connect.NewResponse(&webv1.ListPatternsResponse{Patterns: patterns}), nil
}

func (s *WorkspaceService) GetPattern(_ context.Context, req *connect.Request[webv1.GetPatternRequest]) (*connect.Response[webv1.GetPatternResponse], error) {
	id, runID, patternID, connectErr := patternNameFromResource(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadDiscoveryRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	for _, p := range record.Patterns {
		if p.DirName == patternID {
			return connect.NewResponse(&webv1.GetPatternResponse{Pattern: patternMessage(id, runID, p)}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.Errorf("pattern %q not found", patternID))
}

func (s *WorkspaceService) GetErrorsView(_ context.Context, req *connect.Request[webv1.GetErrorsViewRequest]) (*connect.Response[webv1.GetErrorsViewResponse], error) {
	id, runID, connectErr := discoveryRunNameFromResource(req.Msg.Parent)
	if connectErr != nil {
		return nil, connectErr
	}
	record, err := workspace.ReadDiscoveryRunRecord(s.workspaceDir(id), runID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	response := &webv1.GetErrorsViewResponse{}
	for _, p := range record.Patterns {
		if errorLikePattern.MatchString(p.Template) || errorLikePattern.MatchString(p.SemanticID) {
			response.ErrorPatterns = append(response.ErrorPatterns, &webv1.ErrorPattern{Pattern: patternMessage(id, runID, p)})
		}
	}
	for _, line := range record.Unmatched {
		if errorLikePattern.MatchString(line.Content) {
			response.UnmatchedErrorLines = append(response.UnmatchedErrorLines, &webv1.UnmatchedErrorLine{
				FileName:   line.FileName,
				LineNumber: int32(line.LineNum),
				Content:    line.Content,
			})
		}
	}
	return connect.NewResponse(response), nil
}

func (s *WorkspaceService) workspaceMessage(ctx context.Context, id string) (*webv1.Workspace, error) {
	files, err := s.listLogFileMessages(id)
	if err != nil {
		return nil, err
	}
	records, err := workspace.ListDiscoveryRunRecords(s.workspaceDir(id))
	if err != nil {
		return nil, err
	}
	latest := workspace.LatestDiscoveryRun(records)
	latestSuccess := workspace.LatestSuccessfulDiscoveryRun(records)
	status := webv1.WorkspaceStatus_WORKSPACE_STATUS_EMPTY
	switch {
	case latest != nil && latest.State == workspace.DiscoveryRunStateRunning:
		status = webv1.WorkspaceStatus_WORKSPACE_STATUS_DISCOVERING
	case latest != nil && latest.State == workspace.DiscoveryRunStateFailed:
		status = webv1.WorkspaceStatus_WORKSPACE_STATUS_FAILED
	case latestSuccess != nil:
		status = webv1.WorkspaceStatus_WORKSPACE_STATUS_READY
	case len(files) > 0:
		status = webv1.WorkspaceStatus_WORKSPACE_STATUS_EMPTY
	}
	_ = ctx
	msg := &webv1.Workspace{
		Name:         workspaceName(id),
		WorkspaceId:  id,
		Status:       status,
		LogFileCount: int32(len(files)),
	}
	if latest != nil {
		msg.LatestDiscoveryRun = s.discoveryRunMessage(id, *latest)
	}
	if latestSuccess != nil {
		msg.LatestSuccessfulDiscoveryRun = s.discoveryRunMessage(id, *latestSuccess)
		msg.PatternCount = int32(latestSuccess.PatternCount)
	}
	return msg, nil
}

func (s *WorkspaceService) listLogFileMessages(id string) ([]*webv1.LogFile, error) {
	names, err := workspace.ListLogFiles(s.workspaceDir(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]*webv1.LogFile, 0, len(names))
	for _, name := range names {
		msg, err := s.logFileMessage(id, name)
		if err != nil {
			return nil, err
		}
		files = append(files, msg)
	}
	return files, nil
}

func (s *WorkspaceService) logFileMessage(id, fileName string) (*webv1.LogFile, error) {
	path := filepath.Join(s.workspaceDir(id), "logs", fileName)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &webv1.LogFile{
		Name:      fmt.Sprintf("%s/logFiles/%s", workspaceName(id), fileName),
		FileName:  fileName,
		SizeBytes: info.Size(),
		UpdatedAt: timestamppb.New(info.ModTime()),
	}, nil
}

func (s *WorkspaceService) discoveryRunMessage(workspaceID string, record workspace.DiscoveryRunRecord) *webv1.DiscoveryRun {
	msg := &webv1.DiscoveryRun{
		Name:           fmt.Sprintf("%s/discoveryRuns/%s", workspaceName(workspaceID), record.ID),
		DiscoveryRunId: record.ID,
		State:          discoveryRunState(record.State),
		CurrentStep:    discoveryStep(record.CurrentStep),
		StartedAt:      timestamppb.New(record.StartedAt),
		LogFileCount:   int32(record.LogFileCount),
		PatternCount:   int32(record.PatternCount),
		UnmatchedCount: int32(record.UnmatchedCount),
	}
	if record.Progress != nil {
		msg.Progress = discoveryProgress(record.Progress)
	}
	if record.Error != nil {
		msg.Error = discoveryError(record.Error)
	}
	if record.FinishedAt != nil {
		msg.FinishedAt = timestamppb.New(*record.FinishedAt)
	}
	return msg
}

func discoveryProgress(progress *workspace.DiscoveryRunProgress) *webv1.DiscoveryRunProgress {
	msg := &webv1.DiscoveryRunProgress{
		Step: discoveryStep(progress.Step),
	}
	if progress.LabelBatch != nil {
		msg.LabelBatch = &webv1.DiscoveryLabelBatchProgress{
			Event:          progress.LabelBatch.Event,
			BatchNumber:    int32(progress.LabelBatch.BatchNumber),
			BatchCount:     int32(progress.LabelBatch.BatchCount),
			BatchSize:      int32(progress.LabelBatch.BatchSize),
			Attempt:        int32(progress.LabelBatch.Attempt),
			MaxAttempts:    int32(progress.LabelBatch.MaxAttempts),
			CompletedCount: int32(progress.LabelBatch.CompletedCount),
		}
	}
	return msg
}

func discoveryError(runError *workspace.DiscoveryRunError) *webv1.DiscoveryRunError {
	return &webv1.DiscoveryRunError{
		Code:    runError.Code,
		Message: runError.Message,
		Step:    discoveryStep(runError.Step),
	}
}

func (s *WorkspaceService) workspaceDir(id string) string {
	return filepath.Join(s.root, id)
}

func (s *WorkspaceService) recoverInterruptedDiscoveryRuns() error {
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
		records, err := workspace.ListDiscoveryRunRecords(workspaceDir)
		if err != nil {
			return errors.Errorf("list discovery runs for %s: %w", entry.Name(), err)
		}
		for _, record := range records {
			if !isInterruptedDiscoveryRun(record.State) {
				continue
			}
			if record.CurrentStep == "" {
				record.CurrentStep = workspace.DiscoveryStepReadingLogs
			}
			finishedAt := time.Now().UTC()
			record.State = workspace.DiscoveryRunStateFailed
			record.FinishedAt = &finishedAt
			record.Error = &workspace.DiscoveryRunError{
				Code:    "DISCOVERY_INTERRUPTED",
				Message: "Discovery did not complete before the web server stopped",
				Step:    record.CurrentStep,
			}
			record.ErrorMessage = "Discovery did not complete before the web server stopped"
			if err := workspace.WriteDiscoveryRunRecord(workspaceDir, record); err != nil {
				return errors.Errorf("mark discovery run %s interrupted: %w", record.ID, err)
			}
		}
	}
	return nil
}

func isInterruptedDiscoveryRun(state workspace.DiscoveryRunState) bool {
	return state == workspace.DiscoveryRunStateQueued || state == workspace.DiscoveryRunStateRunning
}

func (s *WorkspaceService) ensureWorkspaceExists(id string) *connect.Error {
	info, err := os.Stat(s.workspaceDir(id))
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewError(connect.CodeNotFound, errors.Errorf("workspace %q not found", id))
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	if !info.IsDir() {
		return connect.NewError(connect.CodeNotFound, errors.Errorf("workspace %q not found", id))
	}
	return nil
}

func (s *WorkspaceService) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *WorkspaceService) reserveDiscovery(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *WorkspaceService) setRunning(id string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		s.running[id] = true
		return
	}
	delete(s.running, id)
}

func patternMessage(workspaceID, runID string, p workspace.PatternInfo) *webv1.Pattern {
	lineRefs := make([]*webv1.LineRef, 0, len(p.LineRefs))
	for _, ref := range p.LineRefs {
		lineRefs = append(lineRefs, lineRefMessage(ref))
	}
	return &webv1.Pattern{
		Name:        fmt.Sprintf("%s/discoveryRuns/%s/patterns/%s", workspaceName(workspaceID), runID, p.DirName),
		PatternId:   p.DirName,
		SemanticId:  p.SemanticID,
		Description: p.Description,
		Template:    p.Template,
		Count:       int32(p.Count),
		FirstSeen:   lineRefMessage(p.FirstSeen),
		LastSeen:    lineRefMessage(p.LastSeen),
		LineRefs:    lineRefs,
		Samples:     append([]string(nil), p.Samples...),
	}
}

func lineRefMessage(ref workspace.LineRef) *webv1.LineRef {
	return &webv1.LineRef{
		FileName:   ref.FileName,
		LineNumber: int32(ref.LineNum),
	}
}

func workspaceName(id string) string {
	return "workspaces/" + id
}

func workspaceIDFromName(name string) (string, *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 2 && parts[0] == "workspaces" && parts[1] != "" {
		return parts[1], nil
	}
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], nil
	}
	return "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid workspace name %q", name))
}

func logFileNameFromResource(name string) (workspaceID, fileName string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 4 && parts[0] == "workspaces" && parts[2] == "logFiles" && parts[1] != "" && parts[3] != "" {
		return parts[1], parts[3], nil
	}
	return "", "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid log file name %q", name))
}

func discoveryRunNameFromResource(name string) (workspaceID, runID string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 4 && parts[0] == "workspaces" && parts[2] == "discoveryRuns" && parts[1] != "" && parts[3] != "" {
		return parts[1], parts[3], nil
	}
	return "", "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid discovery run name %q", name))
}

func patternNameFromResource(name string) (workspaceID, runID, patternID string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 6 && parts[0] == "workspaces" && parts[2] == "discoveryRuns" && parts[4] == "patterns" && parts[1] != "" && parts[3] != "" && parts[5] != "" {
		return parts[1], parts[3], parts[5], nil
	}
	return "", "", "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid pattern name %q", name))
}

func sanitizeWorkspaceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = workspaceIDPattern.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func discoveryRunState(state workspace.DiscoveryRunState) webv1.DiscoveryRunState {
	switch state {
	case workspace.DiscoveryRunStateQueued:
		return webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_QUEUED
	case workspace.DiscoveryRunStateRunning:
		return webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_RUNNING
	case workspace.DiscoveryRunStateSucceeded:
		return webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_SUCCEEDED
	case workspace.DiscoveryRunStateFailed:
		return webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_FAILED
	default:
		return webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_UNSPECIFIED
	}
}

func discoveryStep(step workspace.DiscoveryStep) webv1.DiscoveryStep {
	switch step {
	case workspace.DiscoveryStepReadingLogs:
		return webv1.DiscoveryStep_DISCOVERY_STEP_READING_LOGS
	case workspace.DiscoveryStepMergingEntries:
		return webv1.DiscoveryStep_DISCOVERY_STEP_MERGING_ENTRIES
	case workspace.DiscoveryStepDiscoveringPatterns:
		return webv1.DiscoveryStep_DISCOVERY_STEP_DISCOVERING_PATTERNS
	case workspace.DiscoveryStepLabelingPatterns:
		return webv1.DiscoveryStep_DISCOVERY_STEP_LABELING_PATTERNS
	case workspace.DiscoveryStepWritingResults:
		return webv1.DiscoveryStep_DISCOVERY_STEP_WRITING_RESULTS
	default:
		return webv1.DiscoveryStep_DISCOVERY_STEP_UNSPECIFIED
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
