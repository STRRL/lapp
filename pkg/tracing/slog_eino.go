package tracing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
)

// SlogEinoHandler logs full callback payloads as JSON for side-by-side comparison with tape / Langfuse.
// Stream callbacks only log a placeholder (the StreamReader is not drained here).
type SlogEinoHandler struct {
	Log *slog.Logger
}

func NewSlogEinoHandler(log *slog.Logger) *SlogEinoHandler {
	if log == nil {
		log = slog.Default()
	}
	return &SlogEinoHandler{Log: log}
}

func (h *SlogEinoHandler) Needed(context.Context, *callbacks.RunInfo, callbacks.CallbackTiming) bool {
	return true
}

func (h *SlogEinoHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	h.logPayload(ctx, "eino.callback.OnStart", info, "input", input)
	return ctx
}

func (h *SlogEinoHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	h.logPayload(ctx, "eino.callback.OnEnd", info, "output", output)
	return ctx
}

func (h *SlogEinoHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if err == nil {
		return ctx
	}
	attrs := []any{
		slog.String("err", err.Error()),
	}
	if info != nil {
		attrs = append(attrs,
			slog.String("node", info.Name),
			slog.String("component", string(info.Component)),
			slog.String("graph_type", info.Type),
		)
	}
	h.Log.WarnContext(ctx, "eino.callback.OnError", attrs...)
	return ctx
}

func (h *SlogEinoHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	h.logPayload(ctx, "eino.callback.OnStartWithStreamInput", info, "input_stream", streamInputNote(input))
	return ctx
}

func (h *SlogEinoHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	h.logPayload(ctx, "eino.callback.OnEndWithStreamOutput", info, "output_stream", streamOutputNote(output))
	return ctx
}

func streamInputNote(s *schema.StreamReader[callbacks.CallbackInput]) any {
	if s == nil {
		return map[string]any{"kind": "StreamReader[CallbackInput]", "nil": true}
	}
	return map[string]any{
		"kind": "StreamReader[CallbackInput]",
		"note": "not drained here; compare non-stream OnStart/OnEnd JSON or tape",
	}
}

func streamOutputNote(s *schema.StreamReader[callbacks.CallbackOutput]) any {
	if s == nil {
		return map[string]any{"kind": "StreamReader[CallbackOutput]", "nil": true}
	}
	return map[string]any{
		"kind": "StreamReader[CallbackOutput]",
		"note": "not drained here; compare non-stream callbacks or tape",
	}
}

func (h *SlogEinoHandler) logPayload(ctx context.Context, msg string, info *callbacks.RunInfo, field string, v any) {
	attrs := []any{}
	if info != nil {
		attrs = append(attrs,
			slog.String("node", info.Name),
			slog.String("component", string(info.Component)),
			slog.String("graph_type", info.Type),
		)
	}
	attrs = append(attrs, slog.String(field, marshalCallbackPayload(v)))
	h.Log.InfoContext(ctx, msg, attrs...)
}

func marshalCallbackPayload(v any) string {
	if v == nil {
		return "null"
	}
	b, err := sonic.MarshalString(v)
	if err != nil {
		return fmt.Sprintf("%q", fmt.Sprintf("<sonic.MarshalString: %v type=%T>", err, v))
	}
	return b
}
