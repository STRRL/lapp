package webapp

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/workspace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateImportRunRejectsInvalidRequestWithoutRecord(t *testing.T) {
	root := t.TempDir()
	service, err := NewWorkspaceService(ServiceConfig{
		Root: root,
		ImportFetcher: func(context.Context, workspace.ImportRequest, workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
			t.Fatal("fetcher must not run")
			return workspace.ImportFetchResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	created, err := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: "imports",
	}))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	workspaceName := created.Msg.Workspace.Name
	from := timestamppb.New(time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC))
	to := timestamppb.New(time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC))

	tests := []struct {
		name string
		req  *webv1.CreateImportRunRequest
		code connect.Code
	}{
		{
			name: "blank project",
			req: &webv1.CreateImportRunRequest{
				Parent:  workspaceName,
				Project: " \t ",
				From:    from,
				To:      to,
			},
			code: connect.CodeInvalidArgument,
		},
		{
			name: "missing from",
			req: &webv1.CreateImportRunRequest{
				Parent:  workspaceName,
				Project: "acme-prod",
				To:      to,
			},
			code: connect.CodeInvalidArgument,
		},
		{
			name: "missing to",
			req: &webv1.CreateImportRunRequest{
				Parent:  workspaceName,
				Project: "acme-prod",
				From:    from,
			},
			code: connect.CodeInvalidArgument,
		},
		{
			name: "from after to",
			req: &webv1.CreateImportRunRequest{
				Parent:  workspaceName,
				Project: "acme-prod",
				From:    to,
				To:      from,
			},
			code: connect.CodeInvalidArgument,
		},
		{
			name: "negative limit",
			req: &webv1.CreateImportRunRequest{
				Parent:  workspaceName,
				Project: "acme-prod",
				From:    from,
				To:      to,
				Limit:   -1,
			},
			code: connect.CodeInvalidArgument,
		},
		{
			name: "unknown workspace",
			req: &webv1.CreateImportRunRequest{
				Parent:  "workspaces/missing",
				Project: "acme-prod",
				From:    from,
				To:      to,
			},
			code: connect.CodeNotFound,
		},
		{
			name: "unsafe workspace",
			req: &webv1.CreateImportRunRequest{
				Parent:  "workspaces/..",
				Project: "acme-prod",
				From:    from,
				To:      to,
			},
			code: connect.CodeInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.CreateImportRun(context.Background(), connect.NewRequest(test.req)); connect.CodeOf(err) != test.code {
				t.Fatalf("CreateImportRun code = %s, want %s: %v", connect.CodeOf(err), test.code, err)
			}
			records, err := workspace.ListImportRunRecords(service.workspaceDir("imports"))
			if err != nil {
				t.Fatalf("ListImportRunRecords: %v", err)
			}
			if len(records) != 0 {
				t.Fatalf("validation wrote records: %+v", records)
			}
		})
	}
}

func TestWorkspaceServiceImportRunAsyncSuccess(t *testing.T) {
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	line := `{"ts":"2026-07-25T00:00:01Z","severity":"ERROR","payload":{"message":"failed request"}}`
	service, workspaceName := newImportTestService(t, func(_ context.Context, req workspace.ImportRequest, writeLine workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		if req.Limit != defaultImportLimit {
			t.Errorf("fetch limit = %d, want %d", req.Limit, defaultImportLimit)
		}
		close(fetchStarted)
		<-releaseFetch
		if err := writeLine(line); err != nil {
			return workspace.ImportFetchResult{}, err
		}
		return workspace.ImportFetchResult{}, nil
	})

	response, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(workspaceName)))
	if err != nil {
		t.Fatalf("CreateImportRun: %v", err)
	}
	startedRun := importRunFromOperation(t, response.Msg)
	runName := startedRun.Name
	operationName := response.Msg.Name
	if runName == "" {
		t.Fatal("CreateImportRun returned an empty run name")
	}
	if operationName != workspaceName+"/operations/"+startedRun.ImportRunId {
		t.Fatalf("operation name = %q", operationName)
	}
	if response.Msg.Done {
		t.Fatal("new import operation is already done")
	}
	if startedRun.Limit != defaultImportLimit {
		t.Fatalf("run limit = %d, want %d", startedRun.Limit, defaultImportLimit)
	}

	immediate, err := service.GetImportRun(context.Background(), connect.NewRequest(&webv1.GetImportRunRequest{Name: runName}))
	if err != nil {
		t.Fatalf("GetImportRun immediately after create: %v", err)
	}
	if immediate.Msg.ImportRun.Name != runName {
		t.Fatalf("GetImportRun name = %q, want %q", immediate.Msg.ImportRun.Name, runName)
	}
	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("fetcher did not start")
	}

	close(releaseFetch)
	run := waitForImportRunState(t, service, runName, webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED)
	if run.EntryCount != 1 || run.LogFileName == "" || run.Truncated {
		t.Fatalf("unexpected terminal run: %+v", run)
	}
	record, err := workspace.ReadImportRunRecord(service.workspaceDir("imports"), run.ImportRunId)
	if err != nil {
		t.Fatalf("ReadImportRunRecord: %v", err)
	}
	if int(run.EntryCount) != record.EntryCount || run.LogFileName != record.LogFileName {
		t.Fatalf("response and record disagree: run %+v, record %+v", run, record)
	}
	content, err := os.ReadFile(filepath.Join(service.workspaceDir("imports"), "logs", run.LogFileName))
	if err != nil {
		t.Fatalf("read imported log: %v", err)
	}
	if string(content) != line+"\n" {
		t.Fatalf("imported log content = %q", content)
	}
	completed, err := service.GetOperation(context.Background(), connect.NewRequest(&longrunningpb.GetOperationRequest{Name: operationName}))
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if !completed.Msg.Done || completed.Msg.GetResponse() == nil {
		t.Fatalf("completed import operation = %+v", completed.Msg)
	}
	var result webv1.ImportRun
	if err := completed.Msg.GetResponse().UnmarshalTo(&result); err != nil {
		t.Fatalf("unmarshal import response: %v", err)
	}
	if result.Name != runName {
		t.Fatalf("operation response run = %q, want %q", result.Name, runName)
	}
}

func TestWorkspaceServiceImportRunFailure(t *testing.T) {
	service, workspaceName := newImportTestService(t, func(context.Context, workspace.ImportRequest, workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		return workspace.ImportFetchResult{}, stderrors.New("cloud logging unavailable")
	})

	response, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(workspaceName)))
	if err != nil {
		t.Fatalf("CreateImportRun: %v", err)
	}
	run := waitForImportRunState(t, service, importRunFromOperation(t, response.Msg).Name, webv1.ImportRunState_IMPORT_RUN_STATE_FAILED)
	if run.Error == nil || run.Error.Code != "FETCH_FAILED" {
		t.Fatalf("unexpected import error: %+v", run.Error)
	}
	if run.FinishedAt == nil {
		t.Fatal("failed run has no finished_at")
	}
	failed, err := service.GetOperation(context.Background(), connect.NewRequest(&longrunningpb.GetOperationRequest{Name: response.Msg.Name}))
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if !failed.Msg.Done || failed.Msg.GetError().Code != int32(connect.CodeInternal) {
		t.Fatalf("failed import operation = %+v", failed.Msg)
	}
}

func TestWorkspaceServiceImportRunTruncation(t *testing.T) {
	service, workspaceName := newImportTestService(t, func(_ context.Context, _ workspace.ImportRequest, writeLine workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
		if err := writeLine(`{"payload":{"message":"one"}}`); err != nil {
			return workspace.ImportFetchResult{}, err
		}
		return workspace.ImportFetchResult{Truncated: true}, nil
	})

	response, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(workspaceName)))
	if err != nil {
		t.Fatalf("CreateImportRun: %v", err)
	}
	run := waitForImportRunState(t, service, importRunFromOperation(t, response.Msg).Name, webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED)
	if !run.Truncated {
		t.Fatalf("expected truncated run: %+v", run)
	}
}

func TestWorkspaceServiceListImportRunsNewestFirst(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "imports")
	mustMkdir(t, filepath.Join(workspaceDir, "logs"))
	oldTime := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	writeImportRecord(t, workspaceDir, "old", "old-project", "", oldTime, workspace.ImportRunStateSucceeded)
	writeImportRecord(t, workspaceDir, "same-a", "first-new-project", "", newTime, workspace.ImportRunStateSucceeded)
	writeImportRecord(t, workspaceDir, "same-b", "second-new-project", "", newTime, workspace.ImportRunStateSucceeded)

	service, err := NewWorkspaceService(ServiceConfig{Root: root})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	response, err := service.ListImportRuns(context.Background(), connect.NewRequest(&webv1.ListImportRunsRequest{
		Parent: "workspaces/imports",
	}))
	if err != nil {
		t.Fatalf("ListImportRuns: %v", err)
	}
	if len(response.Msg.ImportRuns) != 3 {
		t.Fatalf("ListImportRuns count = %d, want 3", len(response.Msg.ImportRuns))
	}
	if response.Msg.ImportRuns[0].ImportRunId != "same-b" ||
		response.Msg.ImportRuns[1].ImportRunId != "same-a" ||
		response.Msg.ImportRuns[2].ImportRunId != "old" {
		t.Fatalf("unexpected run order: %+v", response.Msg.ImportRuns)
	}
}

func TestWorkspaceServiceRunMutualExclusion(t *testing.T) {
	importStarted := make(chan struct{})
	releaseImport := make(chan struct{})
	discoveryStarted := make(chan struct{})
	releaseDiscovery := make(chan struct{})
	service, err := NewWorkspaceService(ServiceConfig{
		Root: t.TempDir(),
		ImportFetcher: func(context.Context, workspace.ImportRequest, workspace.ImportLineWriter) (workspace.ImportFetchResult, error) {
			close(importStarted)
			<-releaseImport
			return workspace.ImportFetchResult{}, nil
		},
		Labeler: func(_ context.Context, _ semantic.Config, inputs []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
			close(discoveryStarted)
			<-releaseDiscovery
			labels := make([]semantic.SemanticLabel, 0, len(inputs))
			for _, input := range inputs {
				labels = append(labels, semantic.SemanticLabel{
					PatternUUIDString: input.PatternUUIDString,
					SemanticID:        "request-failed",
					Description:       "Request failed",
				})
			}
			return labels, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	importWorkspace := createWorkspaceWithLog(t, service, "import-busy")
	discoveryWorkspace := createWorkspaceWithLog(t, service, "discovery-busy")

	importResponse, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(importWorkspace)))
	if err != nil {
		t.Fatalf("CreateImportRun: %v", err)
	}
	select {
	case <-importStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("import fetcher did not start")
	}

	failedPreconditions := []struct {
		name string
		code connect.Code
		call func() error
	}{
		{
			name: "second import",
			code: connect.CodeAborted,
			call: func() error {
				_, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(importWorkspace)))
				return err
			},
		},
		{
			name: "discovery",
			code: connect.CodeAborted,
			call: func() error {
				_, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{Parent: importWorkspace}))
				return err
			},
		},
		{
			name: "upload",
			code: connect.CodeFailedPrecondition,
			call: func() error {
				_, err := service.UploadLogFile(context.Background(), connect.NewRequest(&webv1.UploadLogFileRequest{
					Parent:   importWorkspace,
					FileName: "other.log",
					Content:  []byte("other\n"),
				}))
				return err
			},
		},
		{
			name: "delete log",
			code: connect.CodeFailedPrecondition,
			call: func() error {
				_, err := service.DeleteLogFile(context.Background(), connect.NewRequest(&webv1.DeleteLogFileRequest{
					Name: importWorkspace + "/logFiles/app.log",
				}))
				return err
			},
		},
		{
			name: "delete workspace",
			code: connect.CodeFailedPrecondition,
			call: func() error {
				_, err := service.DeleteWorkspace(context.Background(), connect.NewRequest(&webv1.DeleteWorkspaceRequest{Name: importWorkspace}))
				return err
			},
		},
	}
	for _, test := range failedPreconditions {
		t.Run("import blocks "+test.name, func(t *testing.T) {
			if err := test.call(); connect.CodeOf(err) != test.code {
				t.Fatalf("code = %s, want %s: %v", connect.CodeOf(err), test.code, err)
			}
		})
	}

	close(releaseImport)
	waitForImportRunState(t, service, importRunFromOperation(t, importResponse.Msg).Name, webv1.ImportRunState_IMPORT_RUN_STATE_SUCCEEDED)

	discoveryResponse, err := service.CreateDiscoveryRun(context.Background(), connect.NewRequest(&webv1.CreateDiscoveryRunRequest{
		Parent: discoveryWorkspace,
	}))
	if err != nil {
		t.Fatalf("CreateDiscoveryRun: %v", err)
	}
	select {
	case <-discoveryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("discovery labeler did not start")
	}
	if _, err := service.CreateImportRun(context.Background(), connect.NewRequest(validImportRequest(discoveryWorkspace))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("CreateImportRun during discovery code = %s, want aborted: %v", connect.CodeOf(err), err)
	}
	close(releaseDiscovery)
	waitForRunState(t, service, discoveryRunFromOperation(t, discoveryResponse.Msg).Name, webv1.DiscoveryRunState_DISCOVERY_RUN_STATE_SUCCEEDED)
}

func TestNewWorkspaceServiceMarksInterruptedImportRunsFailed(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "imports")
	mustMkdir(t, filepath.Join(workspaceDir, "logs"))
	startedAt := time.Now().Add(-time.Minute).UTC()
	writeImportRecord(t, workspaceDir, "queued", "acme-prod", "severity>=ERROR", startedAt, workspace.ImportRunStateQueued)
	writeImportRecord(t, workspaceDir, "running", "acme-prod", "severity>=ERROR", startedAt, workspace.ImportRunStateRunning)
	writeImportRecord(t, workspaceDir, "succeeded", "acme-prod", "severity>=ERROR", startedAt, workspace.ImportRunStateSucceeded)

	service, err := NewWorkspaceService(ServiceConfig{Root: root})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	for _, id := range []string{"queued", "running"} {
		record, err := workspace.ReadImportRunRecord(workspaceDir, id)
		if err != nil {
			t.Fatalf("ReadImportRunRecord %s: %v", id, err)
		}
		if record.State != workspace.ImportRunStateFailed || record.FinishedAt == nil {
			t.Fatalf("interrupted run %s was not failed: %+v", id, record)
		}
		if record.Error == nil || record.Error.Code != "IMPORT_INTERRUPTED" {
			t.Fatalf("unexpected interrupted error for %s: %+v", id, record.Error)
		}
		operation, err := service.GetOperation(context.Background(), connect.NewRequest(&longrunningpb.GetOperationRequest{
			Name: "workspaces/imports/operations/" + id,
		}))
		if err != nil {
			t.Fatalf("GetOperation %s: %v", id, err)
		}
		if !operation.Msg.Done || operation.Msg.GetError().Code != int32(connect.CodeAborted) {
			t.Fatalf("interrupted operation %s = %+v", id, operation.Msg)
		}
	}
	record, err := workspace.ReadImportRunRecord(workspaceDir, "succeeded")
	if err != nil {
		t.Fatalf("ReadImportRunRecord succeeded: %v", err)
	}
	if record.State != workspace.ImportRunStateSucceeded {
		t.Fatalf("succeeded run changed state: %+v", record)
	}
}

func TestWorkspaceServiceListRecentImportQueries(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "first")
	secondDir := filepath.Join(root, "second")
	mustMkdir(t, filepath.Join(firstDir, "logs"))
	mustMkdir(t, filepath.Join(secondDir, "logs"))
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	writeImportRecord(t, firstDir, "duplicate-old", "acme-prod", "severity>=ERROR", base, workspace.ImportRunStateSucceeded)
	writeImportRecord(t, secondDir, "other", "billing-prod", "resource.type=\"cloud_run_revision\"", base.Add(time.Hour), workspace.ImportRunStateSucceeded)
	writeImportRecord(t, secondDir, "duplicate-new", "acme-prod", "severity>=ERROR", base.Add(2*time.Hour), workspace.ImportRunStateSucceeded)

	service, err := NewWorkspaceService(ServiceConfig{Root: root})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	response, err := service.ListRecentImportQueries(context.Background(), connect.NewRequest(&webv1.ListRecentImportQueriesRequest{}))
	if err != nil {
		t.Fatalf("ListRecentImportQueries: %v", err)
	}
	if len(response.Msg.RecentQueries) != 2 {
		t.Fatalf("recent query count = %d, want 2", len(response.Msg.RecentQueries))
	}
	if response.Msg.RecentQueries[0].Project != "acme-prod" || response.Msg.RecentQueries[0].Filter != "severity>=ERROR" {
		t.Fatalf("unexpected first query: %+v", response.Msg.RecentQueries[0])
	}
	if !response.Msg.RecentQueries[0].LastUsedAt.AsTime().Equal(base.Add(2 * time.Hour)) {
		t.Fatalf("duplicate last_used_at = %s, want %s", response.Msg.RecentQueries[0].LastUsedAt.AsTime(), base.Add(2*time.Hour))
	}
	if response.Msg.RecentQueries[1].Project != "billing-prod" {
		t.Fatalf("unexpected second query: %+v", response.Msg.RecentQueries[1])
	}

	for index := range 25 {
		writeImportRecord(
			t,
			firstDir,
			fmt.Sprintf("extra-%02d", index),
			fmt.Sprintf("project-%02d", index),
			"",
			base.Add(-time.Duration(index+1)*time.Hour),
			workspace.ImportRunStateSucceeded,
		)
	}
	response, err = service.ListRecentImportQueries(context.Background(), connect.NewRequest(&webv1.ListRecentImportQueriesRequest{}))
	if err != nil {
		t.Fatalf("ListRecentImportQueries after extra records: %v", err)
	}
	if len(response.Msg.RecentQueries) != recentImportQueryLimit {
		t.Fatalf("recent query count = %d, want %d", len(response.Msg.RecentQueries), recentImportQueryLimit)
	}
}

func newImportTestService(t *testing.T, fetcher workspace.ImportFetcher) (service *WorkspaceService, name string) {
	t.Helper()
	service, err := NewWorkspaceService(ServiceConfig{
		Root:          t.TempDir(),
		ImportFetcher: fetcher,
	})
	if err != nil {
		t.Fatalf("NewWorkspaceService: %v", err)
	}
	created, err := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: "imports",
	}))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	return service, created.Msg.Workspace.Name
}

func validImportRequest(parent string) *webv1.CreateImportRunRequest {
	return &webv1.CreateImportRunRequest{
		Parent:  parent,
		Project: "acme-prod",
		Filter:  "severity>=ERROR",
		From:    timestamppb.New(time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)),
		To:      timestamppb.New(time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)),
	}
}

func createWorkspaceWithLog(t *testing.T, service *WorkspaceService, id string) string {
	t.Helper()
	created, err := service.CreateWorkspace(context.Background(), connect.NewRequest(&webv1.CreateWorkspaceRequest{
		WorkspaceId: id,
	}))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	name := created.Msg.Workspace.Name
	if _, err := service.UploadLogFile(context.Background(), connect.NewRequest(&webv1.UploadLogFileRequest{
		Parent:   name,
		FileName: "app.log",
		Content:  []byte("2026-07-25T00:00:01Z ERROR request failed id=1\n2026-07-25T00:00:02Z ERROR request failed id=2\n"),
	})); err != nil {
		t.Fatalf("UploadLogFile: %v", err)
	}
	return name
}

func writeImportRecord(t *testing.T, dir, id, project, filter string, startedAt time.Time, state workspace.ImportRunState) {
	t.Helper()
	record := workspace.ImportRunRecord{
		ID:        id,
		Provider:  "gcp",
		Project:   project,
		Filter:    filter,
		From:      startedAt.Add(-time.Hour),
		To:        startedAt,
		Limit:     defaultImportLimit,
		State:     state,
		StartedAt: startedAt,
	}
	if state == workspace.ImportRunStateSucceeded || state == workspace.ImportRunStateFailed {
		finishedAt := startedAt.Add(time.Minute)
		record.FinishedAt = &finishedAt
	}
	if err := workspace.WriteImportRunRecord(dir, record); err != nil {
		t.Fatalf("WriteImportRunRecord: %v", err)
	}
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
