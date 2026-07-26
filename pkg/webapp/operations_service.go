package webapp

import (
	"context"
	"os"
	"strings"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect"
	"github.com/go-errors/errors"
	longrunningpbconnect "github.com/strrl/lapp/gen/go/google/longrunning/longrunningpbconnect"
	webv1 "github.com/strrl/lapp/gen/go/lapp/web/v1"
	"github.com/strrl/lapp/pkg/workspace"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ longrunningpbconnect.OperationsHandler = (*WorkspaceService)(nil)

func (s *WorkspaceService) ListOperations(context.Context, *connect.Request[longrunningpb.ListOperationsRequest]) (*connect.Response[longrunningpb.ListOperationsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("listing operations is not supported"))
}

func (s *WorkspaceService) GetOperation(_ context.Context, req *connect.Request[longrunningpb.GetOperationRequest]) (*connect.Response[longrunningpb.Operation], error) {
	operation, connectErr := s.operationByName(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	return connect.NewResponse(operation), nil
}

func (s *WorkspaceService) DeleteOperation(context.Context, *connect.Request[longrunningpb.DeleteOperationRequest]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("deleting operations is not supported"))
}

func (s *WorkspaceService) CancelOperation(context.Context, *connect.Request[longrunningpb.CancelOperationRequest]) (*connect.Response[emptypb.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("cancelling operations is not supported"))
}

func (s *WorkspaceService) WaitOperation(_ context.Context, req *connect.Request[longrunningpb.WaitOperationRequest]) (*connect.Response[longrunningpb.Operation], error) {
	operation, connectErr := s.operationByName(req.Msg.Name)
	if connectErr != nil {
		return nil, connectErr
	}
	return connect.NewResponse(operation), nil
}

func (s *WorkspaceService) operationByName(name string) (*longrunningpb.Operation, *connect.Error) {
	workspaceID, runID, connectErr := operationNameFromResource(name)
	if connectErr != nil {
		return nil, connectErr
	}

	importRecord, err := workspace.ReadImportRunRecord(s.workspaceDir(workspaceID), runID)
	if err == nil {
		operation, err := s.importRunOperation(workspaceID, importRecord)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return operation, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	discoveryRecord, err := workspace.ReadDiscoveryRunRecord(s.workspaceDir(workspaceID), runID)
	if err == nil {
		operation, err := s.discoveryRunOperation(workspaceID, discoveryRecord)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return operation, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.Errorf("operation %q not found", name))
}

func (s *WorkspaceService) importRunOperation(workspaceID string, record workspace.ImportRunRecord) (*longrunningpb.Operation, error) {
	run, err := s.importRunMessage(workspaceID, record)
	if err != nil {
		return nil, err
	}
	metadata, err := anypb.New(&webv1.ImportRunMetadata{ImportRun: run})
	if err != nil {
		return nil, err
	}
	operation := &longrunningpb.Operation{
		Name:     operationName(workspaceID, record.ID),
		Metadata: metadata,
	}
	switch record.State {
	case workspace.ImportRunStateQueued, workspace.ImportRunStateRunning:
		return operation, nil
	case workspace.ImportRunStateSucceeded:
		response, err := anypb.New(run)
		if err != nil {
			return nil, err
		}
		operation.Done = true
		operation.Result = &longrunningpb.Operation_Response{Response: response}
	case workspace.ImportRunStateFailed:
		code := "IMPORT_FAILED"
		message := "Import failed"
		if record.Error != nil {
			code = record.Error.Code
			message = record.Error.Message
		}
		operation.Done = true
		operation.Result = &longrunningpb.Operation_Error{Error: operationError(code, message)}
	}
	return operation, nil
}

func (s *WorkspaceService) discoveryRunOperation(workspaceID string, record workspace.DiscoveryRunRecord) (*longrunningpb.Operation, error) {
	run := s.discoveryRunMessage(workspaceID, record)
	metadata, err := anypb.New(&webv1.DiscoveryRunMetadata{DiscoveryRun: run})
	if err != nil {
		return nil, err
	}
	operation := &longrunningpb.Operation{
		Name:     operationName(workspaceID, record.ID),
		Metadata: metadata,
	}
	switch record.State {
	case workspace.DiscoveryRunStateQueued, workspace.DiscoveryRunStateRunning:
		return operation, nil
	case workspace.DiscoveryRunStateSucceeded:
		response, err := anypb.New(run)
		if err != nil {
			return nil, err
		}
		operation.Done = true
		operation.Result = &longrunningpb.Operation_Response{Response: response}
	case workspace.DiscoveryRunStateFailed:
		code := "DISCOVERY_FAILED"
		message := "Discovery failed"
		if record.Error != nil {
			code = record.Error.Code
			message = record.Error.Message
		}
		operation.Done = true
		operation.Result = &longrunningpb.Operation_Error{Error: operationError(code, message)}
	}
	return operation, nil
}

func operationError(errorCode, message string) *statuspb.Status {
	code := connect.CodeInternal
	if strings.HasSuffix(errorCode, "_INTERRUPTED") {
		code = connect.CodeAborted
	}
	return &statuspb.Status{
		Code:    int32(code),
		Message: message,
	}
}

func operationName(workspaceID, runID string) string {
	return workspaceName(workspaceID) + "/operations/" + runID
}

func operationNameFromResource(name string) (workspaceID, runID string, err *connect.Error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) == 4 && parts[0] == "workspaces" && parts[2] == "operations" && validResourcePart(parts[1]) && validResourcePart(parts[3]) {
		return parts[1], parts[3], nil
	}
	return "", "", connect.NewError(connect.CodeInvalidArgument, errors.Errorf("invalid operation name %q", name))
}
