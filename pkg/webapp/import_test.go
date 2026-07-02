package webapp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/go-errors/errors"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/gcplog"
	"github.com/strrl/lapp/pkg/workspace"
)

type fakeFetcher struct {
	lines   []string
	err     error
	lastCfg gcplog.FetchConfig
	release chan struct{}
}

func (f *fakeFetcher) Fetch(_ context.Context, cfg gcplog.FetchConfig, w io.Writer) (int, error) {
	f.lastCfg = cfg
	if f.release != nil {
		<-f.release
	}
	if f.err != nil {
		return 0, f.err
	}
	for _, line := range f.lines {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return 0, err
		}
	}
	return len(f.lines), nil
}

func newImportTestService(t *testing.T, fetcher gcplog.Fetcher) (service *WorkspaceService, workspaceName string) {
	t.Helper()
	service, err := NewWorkspaceService(ServiceConfig{
		Root:       t.TempDir(),
		GcpFetcher: fetcher,
	})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	created, createErr := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: "gcp-import",
	}))
	if createErr != nil {
		t.Fatalf("CreateWorkspace: %v", createErr)
	}
	return service, created.Msg.Workspace.Name
}

func importLogs(t *testing.T, service *WorkspaceService, parent string) *webv1.ImportRun {
	t.Helper()
	response, err := service.ImportLogs(context.Background(), connect.NewRequest(&webv1.ImportLogsRequest{
		Parent: parent,
		Source: &webv1.ImportLogsRequest_GcpLogging{
			GcpLogging: &webv1.GcpLoggingSource{
				ProjectId: "my-project",
				Filter:    "severity>=ERROR",
			},
		},
	}))
	if err != nil {
		t.Fatalf("ImportLogs: %v", err)
	}
	return response.Msg.ImportRun
}

func waitForImportRunState(t *testing.T, service *WorkspaceService, runName string, state webv1.ImportRunState) *webv1.ImportRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := service.GetImportRun(context.Background(), connect.NewRequest(&webv1.GetImportRunRequest{Name: runName}))
		if err != nil {
			t.Fatalf("GetImportRun: %v", err)
		}
		if response.Msg.ImportRun.State == state {
			return response.Msg.ImportRun
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("import run did not reach %s", state)
	return nil
}

func TestImportLogsSuccessLandsFileInLogs(t *testing.T) {
	fetcher := &fakeFetcher{lines: []string{
		"2026-07-01T10:00:00Z ERROR db timeout user=42",
		"2026-07-01T10:00:01Z ERROR db timeout user=43",
	}}
	service, workspaceName := newImportTestService(t, fetcher)

	run := importLogs(t, service, workspaceName)
	if run.State != webv1.ImportRunState_IMPORT_RUN_STATE_RUNNING {
		t.Fatalf("expected running import, got %s", run.State)
	}
	if run.Source != workspace.ImportSourceGCPLogging {
		t.Fatalf("source = %q", run.Source)
	}

	final := waitForImportRunState(t, service, run.Name, webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED)
	if final.EntryCount != 2 {
		t.Fatalf("entry count = %d, want 2", final.EntryCount)
	}
	if final.LogFileName == "" {
		t.Fatal("expected log file name")
	}

	data, err := os.ReadFile(filepath.Join(service.workspaceDir("gcp-import"), "logs", final.LogFileName))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if string(data) != "2026-07-01T10:00:00Z ERROR db timeout user=42\n2026-07-01T10:00:01Z ERROR db timeout user=43\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}

	files, err := service.ListLogFiles(context.Background(), connect.NewRequest(&webv1.ListLogFilesRequest{Parent: workspaceName}))
	if err != nil {
		t.Fatalf("ListLogFiles: %v", err)
	}
	if len(files.Msg.LogFiles) != 1 || files.Msg.LogFiles[0].FileName != final.LogFileName {
		t.Fatalf("unexpected log files: %+v", files.Msg.LogFiles)
	}

	if fetcher.lastCfg.ProjectID != "my-project" || fetcher.lastCfg.Filter != "severity>=ERROR" {
		t.Fatalf("unexpected fetch config: %+v", fetcher.lastCfg)
	}
	if fetcher.lastCfg.Limit != defaultImportLimit {
		t.Fatalf("limit = %d, want default %d", fetcher.lastCfg.Limit, defaultImportLimit)
	}
}

func TestImportLogsFetchFailureMarksRunFailed(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("permission denied")}
	service, workspaceName := newImportTestService(t, fetcher)

	run := importLogs(t, service, workspaceName)
	final := waitForImportRunState(t, service, run.Name, webv1.ImportRunState_IMPORT_RUN_STATE_FAILED)
	if final.Error == nil || final.Error.Code != "GCP_FETCH_FAILED" {
		t.Fatalf("unexpected error: %+v", final.Error)
	}

	files, err := service.ListLogFiles(context.Background(), connect.NewRequest(&webv1.ListLogFilesRequest{Parent: workspaceName}))
	if err != nil {
		t.Fatalf("ListLogFiles: %v", err)
	}
	if len(files.Msg.LogFiles) != 0 {
		t.Fatalf("expected no log files, got %+v", files.Msg.LogFiles)
	}
}

func TestImportLogsZeroEntriesMarksRunFailed(t *testing.T) {
	fetcher := &fakeFetcher{}
	service, workspaceName := newImportTestService(t, fetcher)

	run := importLogs(t, service, workspaceName)
	final := waitForImportRunState(t, service, run.Name, webv1.ImportRunState_IMPORT_RUN_STATE_FAILED)
	if final.Error == nil || final.Error.Code != "IMPORT_NO_ENTRIES" {
		t.Fatalf("unexpected error: %+v", final.Error)
	}
}

func TestImportLogsBlocksConcurrentJobs(t *testing.T) {
	fetcher := &fakeFetcher{
		lines:   []string{"2026-07-01T10:00:00Z ERROR boom"},
		release: make(chan struct{}),
	}
	service, workspaceName := newImportTestService(t, fetcher)

	run := importLogs(t, service, workspaceName)

	if _, err := service.ImportLogs(context.Background(), connect.NewRequest(&webv1.ImportLogsRequest{
		Parent: workspaceName,
		Source: &webv1.ImportLogsRequest_GcpLogging{
			GcpLogging: &webv1.GcpLoggingSource{ProjectId: "my-project"},
		},
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected concurrent import to be rejected, got %v", err)
	}
	if _, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: workspaceName,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected discovery during import to be rejected, got %v", err)
	}

	close(fetcher.release)
	waitForImportRunState(t, service, run.Name, webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED)
}

func TestImportLogsRejectsInvalidRequests(t *testing.T) {
	service, workspaceName := newImportTestService(t, &fakeFetcher{})

	if _, err := service.ImportLogs(context.Background(), connect.NewRequest(&webv1.ImportLogsRequest{
		Parent: workspaceName,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected missing source to be rejected, got %v", err)
	}

	if _, err := service.ImportLogs(context.Background(), connect.NewRequest(&webv1.ImportLogsRequest{
		Parent: workspaceName,
		Source: &webv1.ImportLogsRequest_GcpLogging{
			GcpLogging: &webv1.GcpLoggingSource{ProjectId: "  "},
		},
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected missing project to be rejected, got %v", err)
	}
}

func TestNewWorkspaceServiceMarksInterruptedImportRunsFailed(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "gcp-import")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := workspace.ImportRunRecord{
		ID:        "run-1",
		State:     workspace.ImportRunStateRunning,
		Source:    workspace.ImportSourceGCPLogging,
		StartedAt: time.Now().UTC(),
	}
	if err := workspace.WriteImportRunRecord(workspaceDir, record); err != nil {
		t.Fatal(err)
	}

	if _, err := NewWorkspaceService(ServiceConfig{Root: root, GcpFetcher: &fakeFetcher{}}); err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}

	got, err := workspace.ReadImportRunRecord(workspaceDir, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workspace.ImportRunStateFailed {
		t.Fatalf("state = %s, want FAILED", got.State)
	}
	if got.Error == nil || got.Error.Code != "IMPORT_INTERRUPTED" {
		t.Fatalf("unexpected error: %+v", got.Error)
	}
}
