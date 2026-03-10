package analyzer

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	einoacp "github.com/strrl/eino-acp"
)

var _ model.ToolCallingChatModel = (*acpToolCallingModel)(nil)

// acpToolCallingModel adapts eino-acp ChatModel to ToolCallingChatModel.
// ACP agents manage tools in their own runtime, so WithTools is a no-op.
type acpToolCallingModel struct {
	base *einoacp.ChatModel
}

func newACPToolCallingModel(base *einoacp.ChatModel) model.ToolCallingChatModel {
	return &acpToolCallingModel{base: base}
}

func (m *acpToolCallingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(ctx, input, opts...)
}

func (m *acpToolCallingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(ctx, input, opts...)
}

func (m *acpToolCallingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
