package tape

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// NewHandler creates an eino callbacks.Handler that records tape entries to the given store.
func NewHandler(store Recorder) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			onStart(store, info, input)
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			onEnd(store, info, output)
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			onError(store, info, err)
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			onEndWithStream(store, info, output)
			return ctx
		}).
		Build()
}

func componentMeta(info *callbacks.RunInfo) map[string]any {
	return map[string]any{
		"component": string(info.Component),
		"name":      info.Name,
		"type":      info.Type,
	}
}

func onStart(store Recorder, info *callbacks.RunInfo, input callbacks.CallbackInput) {
	meta := componentMeta(info)

	if info.Component == components.ComponentOfChatModel {
		modelInput := model.ConvCallbackInput(input)
		if modelInput != nil {
			recordModelInput(store, modelInput, meta)
			return
		}
	}

	if info.Component == components.ComponentOfTool {
		entry := EventEntry("tool_start", map[string]any{
			"component": string(info.Component),
			"name":      info.Name,
		}, meta)
		appendSafe(store, entry)
		return
	}

	entry := EventEntry("start", map[string]any{
		"component": string(info.Component),
		"name":      info.Name,
	}, meta)
	appendSafe(store, entry)
}

func onEnd(store Recorder, info *callbacks.RunInfo, output callbacks.CallbackOutput) {
	meta := componentMeta(info)

	if info.Component == components.ComponentOfChatModel {
		modelOutput := model.ConvCallbackOutput(output)
		if modelOutput != nil {
			recordModelOutput(store, modelOutput, meta)
			return
		}
	}

	if info.Component == components.ComponentOfTool {
		recordToolResult(store, output, meta)
		return
	}

	entry := EventEntry("end", map[string]any{
		"component": string(info.Component),
		"name":      info.Name,
	}, meta)
	appendSafe(store, entry)
}

func onError(store Recorder, info *callbacks.RunInfo, err error) {
	meta := componentMeta(info)
	entry := ErrorEntry(string(info.Component), err.Error(), meta)
	appendSafe(store, entry)
}

func onEndWithStream(store Recorder, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) {
	meta := componentMeta(info)

	// Consume the stream in a goroutine to avoid blocking
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("tape: panic consuming stream", "recover", r)
			}
		}()

		var lastOutput callbacks.CallbackOutput
		for {
			chunk, err := output.Recv()
			if err != nil {
				break
			}
			lastOutput = chunk
		}
		output.Close()

		if lastOutput == nil {
			return
		}

		if info.Component == components.ComponentOfChatModel {
			modelOutput := model.ConvCallbackOutput(lastOutput)
			if modelOutput != nil {
				recordModelOutput(store, modelOutput, meta)
				return
			}
		}

		entry := EventEntry("end", map[string]any{
			"component": string(info.Component),
			"name":      info.Name,
		}, meta)
		appendSafe(store, entry)
	}()
}

func recordModelInput(store Recorder, input *model.CallbackInput, meta map[string]any) {
	// Record system message separately if present
	for _, msg := range input.Messages {
		if msg.Role == schema.System {
			appendSafe(store, SystemEntry(msg.Content, meta))
			continue
		}
		appendSafe(store, MessageEntry(string(msg.Role), msg.Content, meta))
	}

	// Record tool calls from messages
	if len(input.Tools) > 0 {
		var tools []map[string]any
		for _, t := range input.Tools {
			tools = append(tools, map[string]any{
				"name": t.Name,
				"desc": t.Desc,
			})
		}
		appendSafe(store, EventEntry("tools_available", map[string]any{
			"tools": tools,
		}, meta))
	}
}

func recordModelOutput(store Recorder, output *model.CallbackOutput, meta map[string]any) {
	if output.TokenUsage != nil {
		meta["token_usage"] = map[string]any{
			"prompt_tokens":     output.TokenUsage.PromptTokens,
			"completion_tokens": output.TokenUsage.CompletionTokens,
			"total_tokens":      output.TokenUsage.TotalTokens,
		}
	}

	if output.Message != nil {
		// Record tool calls if present
		if len(output.Message.ToolCalls) > 0 {
			var calls []map[string]any
			for _, tc := range output.Message.ToolCalls {
				calls = append(calls, map[string]any{
					"id":       tc.ID,
					"function": tc.Function.Name,
					"args":     tc.Function.Arguments,
				})
			}
			appendSafe(store, ToolCallEntry(calls, meta))
		}

		// Record assistant message
		if output.Message.Content != "" {
			appendSafe(store, MessageEntry(string(output.Message.Role), output.Message.Content, meta))
		}
	}
}

func recordToolResult(store Recorder, output callbacks.CallbackOutput, meta map[string]any) {
	// Tool output is typically a string
	if s, ok := output.(string); ok {
		appendSafe(store, ToolResultEntry([]any{s}, meta))
		return
	}
	appendSafe(store, ToolResultEntry([]any{output}, meta))
}

func appendSafe(store Recorder, entry Entry) {
	if err := store.Append(entry); err != nil {
		slog.Warn("tape: failed to append entry", "err", err, "kind", entry.Kind)
	}
}
