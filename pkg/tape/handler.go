package tape

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type toolCtxKey struct{}

type pendingTool struct {
	Name string
	Args string
}

// NewHandler creates an eino callbacks.Handler that records tape entries to the given store.
func NewHandler(store Recorder) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			return onStart(ctx, store, info, input)
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			onEnd(ctx, store, info, output)
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

func onStart(ctx context.Context, store Recorder, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	meta := componentMeta(info)

	if info.Component == components.ComponentOfChatModel {
		modelInput := model.ConvCallbackInput(input)
		if modelInput != nil {
			recordModelInput(store, modelInput, meta)
			return ctx
		}
	}

	if info.Component == components.ComponentOfTool {
		tIn := tool.ConvCallbackInput(input)
		if tIn != nil {
			return context.WithValue(ctx, toolCtxKey{}, &pendingTool{
				Name: info.Name,
				Args: tIn.ArgumentsInJSON,
			})
		}
		return ctx
	}

	entry := EventEntry("start", map[string]any{
		"component": string(info.Component),
		"name":      info.Name,
	}, meta)
	appendSafe(store, entry)
	return ctx
}

func onEnd(ctx context.Context, store Recorder, info *callbacks.RunInfo, output callbacks.CallbackOutput) {
	meta := componentMeta(info)

	if info.Component == components.ComponentOfChatModel {
		modelOutput := model.ConvCallbackOutput(output)
		if modelOutput != nil {
			recordModelOutput(store, modelOutput, meta)
			return
		}
	}

	if info.Component == components.ComponentOfTool {
		recordToolResult(ctx, store, info, output, meta)
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
	for _, msg := range input.Messages {
		if msg.Role == schema.System {
			appendSafe(store, SystemEntry(msg.Content, meta))
			continue
		}
		appendSafe(store, MessageEntry(string(msg.Role), msg.Content, meta))
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

		if output.Message.Content != "" {
			appendSafe(store, MessageEntry(string(output.Message.Role), output.Message.Content, meta))
		}
	}

	// Write a run event with usage summary
	runData := map[string]any{"status": "ok"}
	if output.TokenUsage != nil {
		runData["usage"] = map[string]any{
			"prompt_tokens":     output.TokenUsage.PromptTokens,
			"completion_tokens": output.TokenUsage.CompletionTokens,
			"total_tokens":      output.TokenUsage.TotalTokens,
		}
	}
	appendSafe(store, EventEntry("run", runData, meta))
}

func recordToolResult(ctx context.Context, store Recorder, info *callbacks.RunInfo, output callbacks.CallbackOutput, meta map[string]any) {
	pend, _ := ctx.Value(toolCtxKey{}).(*pendingTool)
	name := info.Name
	args := ""
	if pend != nil {
		name = pend.Name
		args = pend.Args
	}

	tOut := tool.ConvCallbackOutput(output)
	res := ""
	if tOut != nil {
		if tOut.Response != "" {
			res = tOut.Response
		} else if tOut.ToolOutput != nil {
			if b, err := json.Marshal(tOut.ToolOutput); err == nil {
				res = string(b)
			}
		}
	}

	appendSafe(store, ToolCallEntry([]map[string]any{{
		"name":      name,
		"arguments": args,
	}}, meta))
	appendSafe(store, ToolResultEntry([]any{res}, meta))
}

func appendSafe(store Recorder, entry Entry) {
	if err := store.Append(entry); err != nil {
		slog.Warn("tape: failed to append entry", "err", err, "kind", entry.Kind)
	}
}
