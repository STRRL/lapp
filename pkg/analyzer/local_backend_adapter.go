package analyzer

import (
	"context"

	"github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/schema"
)

var _ filesystem.Backend = (*localBackendAdapter)(nil)
var _ filesystem.StreamingShell = (*localBackendAdapter)(nil)

type localBackendAdapter struct {
	base *local.Local
}

func newLocalBackendAdapter(base *local.Local) *localBackendAdapter {
	return &localBackendAdapter{base: base}
}

func (a *localBackendAdapter) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	return a.base.LsInfo(ctx, req)
}

func (a *localBackendAdapter) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	content, err := a.base.Read(ctx, req)
	if err != nil {
		return nil, err
	}
	return &filesystem.FileContent{Content: content}, nil
}

func (a *localBackendAdapter) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	return a.base.GrepRaw(ctx, req)
}

func (a *localBackendAdapter) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	return a.base.GlobInfo(ctx, req)
}

func (a *localBackendAdapter) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	return a.base.Write(ctx, req)
}

func (a *localBackendAdapter) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	return a.base.Edit(ctx, req)
}

func (a *localBackendAdapter) ExecuteStreaming(ctx context.Context, input *filesystem.ExecuteRequest) (*schema.StreamReader[*filesystem.ExecuteResponse], error) {
	return a.base.ExecuteStreaming(ctx, input)
}
