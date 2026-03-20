package tape

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// NewSlogHandler creates an eino callbacks.Handler that logs all callback
// events directly via slog. Register it with callbacks.AppendGlobalHandlers.
func NewSlogHandler() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(slogOnStart).
		OnEndFn(slogOnEnd).
		OnErrorFn(slogOnError).
		OnEndWithStreamOutputFn(slogOnEndWithStream).
		Build()
}

func slogOnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if info.Component == components.ComponentOfChatModel {
		if mi := model.ConvCallbackInput(input); mi != nil {
			for _, msg := range mi.Messages {
				slog.Info("tape.start",
					"component", string(info.Component),
					"name", info.Name,
					"role", string(msg.Role),
					"content", truncate(msg.Content, 200),
					"tool_calls", len(msg.ToolCalls),
				)
			}
			return ctx
		}
	}

	slog.Info("tape.start",
		"component", string(info.Component),
		"name", info.Name,
		"type", info.Type,
		"input", formatAny(input),
	)
	return ctx
}

func slogOnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if info.Component == components.ComponentOfChatModel {
		if mo := model.ConvCallbackOutput(output); mo != nil {
			attrs := []any{
				"component", string(info.Component),
				"name", info.Name,
			}
			if mo.Message != nil {
				attrs = append(attrs,
					"role", string(mo.Message.Role),
					"content", truncate(mo.Message.Content, 200),
					"tool_calls", len(mo.Message.ToolCalls),
				)
			}
			if mo.TokenUsage != nil {
				attrs = append(attrs,
					"prompt_tokens", mo.TokenUsage.PromptTokens,
					"completion_tokens", mo.TokenUsage.CompletionTokens,
					"total_tokens", mo.TokenUsage.TotalTokens,
				)
			}
			slog.Info("tape.end", attrs...)
			return ctx
		}
	}

	slog.Info("tape.end",
		"component", string(info.Component),
		"name", info.Name,
		"type", info.Type,
		"output", formatAny(output),
	)
	return ctx
}

func slogOnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	slog.Error("tape.error",
		"component", string(info.Component),
		"name", info.Name,
		"err", err,
	)
	return ctx
}

func slogOnEndWithStream(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("tape.stream: panic", "recover", r)
			}
		}()

		var last callbacks.CallbackOutput
		for {
			chunk, err := output.Recv()
			if err != nil {
				break
			}
			last = chunk
		}
		output.Close()

		if last == nil {
			return
		}

		slogOnEnd(context.Background(), info, last)
	}()
	return ctx
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

func formatAny(v any) string {
	if v == nil {
		return "<nil>"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return truncate(string(b), 300)
}
