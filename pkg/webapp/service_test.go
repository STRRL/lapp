package webapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/workspace"
)

func TestWorkspaceServiceDiscoveryFlow(t *testing.T) {
	done := make(chan struct{})
	service, err := NewWorkspaceService(ServiceConfig{
		Root: t.TempDir(),
		Labeler: func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			<-done
			labels := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				labels = append(labels, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "db-timeout-error",
					Description:       "Database timeout for a user request",
				})
			}
			return labels, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}

	createWorkspace, err := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: "Payment Timeout",
	}))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	workspaceName := createWorkspace.Msg.Workspace.Name

	if _, err := service.UploadLogFile(context.Background(), connect.NewRequest(&webv1.UploadLogFileRequest{
		Parent:   workspaceName,
		FileName: "app.log",
		Content:  []byte("2026-06-06 10:00:00 INFO server started port=8080\n2026-06-06 10:00:01 ERROR db timeout user=42\n2026-06-06 10:00:02 ERROR db timeout user=43\n"),
	})); err != nil {
		t.Fatalf("UploadLogFile: %v", err)
	}
	if _, err := service.UploadLogFile(context.Background(), connect.NewRequest(&webv1.UploadLogFileRequest{
		Parent:   workspaceName,
		FileName: "app.log",
		Content:  []byte("duplicate\n"),
	})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("expected duplicate upload to be rejected, got %v", err)
	}

	runResponse, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: workspaceName,
	}))
	if err != nil {
		t.Fatalf("CreateDiscoveryRun: %v", err)
	}
	runName := runResponse.Msg.DiscoveryRun.Name
	if runResponse.Msg.DiscoveryRun.State != webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_RUNNING {
		t.Fatalf("expected running run, got %s", runResponse.Msg.DiscoveryRun.State)
	}
	if _, err := service.DeleteLogFile(context.Background(), connect.NewRequest(&webv1.DeleteLogFileRequest{
		Name: workspaceName + "/logFiles/app.log",
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected delete during discovery to be rejected, got %v", err)
	}
	if _, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: workspaceName,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected concurrent discovery to be rejected, got %v", err)
	}

	close(done)
	waitForRunState(t, service, runName, webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_SUCCEEDED)

	patterns, err := service.ListPatterns(context.Background(), connect.NewRequest(&webv1.ListPatternsRequest{
		Parent: runName,
	}))
	if err != nil {
		t.Fatalf("ListPatterns: %v", err)
	}
	if len(patterns.Msg.Patterns) != 1 || patterns.Msg.Patterns[0].PatternId != "db-timeout-error" {
		t.Fatalf("unexpected patterns: %+v", patterns.Msg.Patterns)
	}

	errorsView, err := service.GetErrorsView(context.Background(), connect.NewRequest(&webv1.GetErrorsViewRequest{
		Parent: runName,
	}))
	if err != nil {
		t.Fatalf("GetErrorsView: %v", err)
	}
	if len(errorsView.Msg.ErrorPatterns) != 1 {
		t.Fatalf("expected one error pattern, got %+v", errorsView.Msg.ErrorPatterns)
	}

	records, err := workspace.ListDiscoveryRunRecords(service.workspaceDir("payment-timeout"))
	if err != nil {
		t.Fatalf("ListDiscoveryRunRecords: %v", err)
	}
	if len(records) != 1 || records[0].State != workspace.DiscoveryRunStateSucceeded {
		t.Fatalf("unexpected records: %+v", records)
	}
}

func TestWorkspaceServiceRejectsMissingWorkspaceMutations(t *testing.T) {
	root := t.TempDir()
	service, err := NewWorkspaceService(ServiceConfig{Root: root})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}

	if _, err := service.UploadLogFile(context.Background(), connect.NewRequest(&webv1.UploadLogFileRequest{
		Parent:   "workspaces/missing",
		FileName: "app.log",
		Content:  []byte("hello\n"),
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected upload to missing workspace to be rejected, got %v", err)
	}
	if _, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: "workspaces/missing",
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected discovery for missing workspace to be rejected, got %v", err)
	}
	if _, err := service.ListLogFiles(context.Background(), connect.NewRequest(&webv1.ListLogFilesRequest{
		Parent: "workspaces/missing",
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected list logs for missing workspace to be rejected, got %v", err)
	}
}

func TestWorkspaceServiceRejectsDiscoveryWithoutLogFiles(t *testing.T) {
	service, err := NewWorkspaceService(ServiceConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	created, err := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: "empty",
	}))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if _, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: created.Msg.Workspace.Name,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected discovery without log files to be rejected, got %v", err)
	}
}

func TestNewWorkspaceServiceMarksInterruptedDiscoveryRunsFailed(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "payment-timeout")
	mustMkdir(t, filepath.Join(workspaceDir, "logs"))

	startedAt := time.Now().Add(-time.Minute).UTC()
	if err := workspace.WriteDiscoveryRunRecord(workspaceDir, workspace.DiscoveryRunRecord{
		ID:              "01900000-0000-7000-8000-000000000001",
		State:           workspace.DiscoveryRunStateRunning,
		CurrentStep:     workspace.DiscoveryStepLabelingPatterns,
		ProgressMessage: "Labeling discovered patterns",
		StartedAt:       startedAt,
	}); err != nil {
		t.Fatalf("WriteDiscoveryRunRecord running: %v", err)
	}
	if err := workspace.WriteDiscoveryRunRecord(workspaceDir, workspace.DiscoveryRunRecord{
		ID:              "01900000-0000-7000-8000-000000000002",
		State:           workspace.DiscoveryRunStateQueued,
		ProgressMessage: "Discovery is queued",
		StartedAt:       startedAt,
	}); err != nil {
		t.Fatalf("WriteDiscoveryRunRecord queued: %v", err)
	}
	if err := workspace.WriteDiscoveryRunRecord(workspaceDir, workspace.DiscoveryRunRecord{
		ID:              "01900000-0000-7000-8000-000000000003",
		State:           workspace.DiscoveryRunStateSucceeded,
		ProgressMessage: "Discovery completed",
		StartedAt:       startedAt,
	}); err != nil {
		t.Fatalf("WriteDiscoveryRunRecord succeeded: %v", err)
	}

	if _, err := NewWorkspaceService(ServiceConfig{Root: root}); err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}

	for _, runID := range []string{
		"01900000-0000-7000-8000-000000000001",
		"01900000-0000-7000-8000-000000000002",
	} {
		record, err := workspace.ReadDiscoveryRunRecord(workspaceDir, runID)
		if err != nil {
			t.Fatalf("ReadDiscoveryRunRecord %s: %v", runID, err)
		}
		if record.State != workspace.DiscoveryRunStateFailed || record.FinishedAt == nil {
			t.Fatalf("expected interrupted run %s to be failed, got %+v", runID, record)
		}
	}
	record, err := workspace.ReadDiscoveryRunRecord(workspaceDir, "01900000-0000-7000-8000-000000000003")
	if err != nil {
		t.Fatalf("ReadDiscoveryRunRecord succeeded: %v", err)
	}
	if record.State != workspace.DiscoveryRunStateSucceeded {
		t.Fatalf("expected successful run to stay succeeded, got %+v", record)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func waitForRunState(t *testing.T, service *WorkspaceService, runName string, state webv1.DiscoveryRunState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := service.GetDiscoveryRun(context.Background(), connect.NewRequest(&webv1.GetDiscoveryRunRequest{Name: runName}))
		if err != nil {
			t.Fatalf("GetDiscoveryRun: %v", err)
		}
		if response.Msg.DiscoveryRun.State == state {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("discovery run did not reach %s", state)
}
