package analyzer

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	einoacp "github.com/strrl/eino-acp"
)

var (
	_ model.ToolCallingChatModel = (*acpToolCallingModel)(nil)
	_ components.Checker         = (*acpToolCallingModel)(nil)
)

// acpToolCallingModel adapts eino-acp ChatModel to ToolCallingChatModel.
// ACP agents manage tools in their own runtime, so WithTools is a no-op.
type acpToolCallingModel struct {
	base *einoacp.ChatModel
}

func newACPToolCallingModel(base *einoacp.ChatModel) model.ToolCallingChatModel {
	return &acpToolCallingModel{base: base}
}

func (m *acpToolCallingModel) IsCallbacksEnabled() bool {
	return true
}

// Generate collects the agent's streamed text. ACP is streaming-only; tool
// calls run inside the agent's own runtime, so only the assistant text is kept.
func (m *acpToolCallingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	stream, err := m.base.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var text strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk != nil {
			text.WriteString(chunk.Content)
		}
	}
	return schema.AssistantMessage(text.String(), nil), nil
}

func (m *acpToolCallingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(ctx, input, opts...)
}

func (m *acpToolCallingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
