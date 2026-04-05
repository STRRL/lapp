package tape

import (
	"context"
	"errors"
	"io"
	"log"
	"runtime/debug"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	goerrors "github.com/go-errors/errors"
)

type RunMeta struct {
	RunID    string
	Provider string
	Model    string
}

type toolCtxKey struct{}

type pendingTool struct {
	Name string
	Args string
}

type EinoHandler struct {
	store       *JSONLStore
	meta        RunMeta
	recordedMsg int
}

func NewEinoHandler(store *JSONLStore, meta RunMeta) *EinoHandler {
	return &EinoHandler{store: store, meta: meta}
}

func (h *EinoHandler) baseMeta() map[string]any {
	m := map[string]any{"run_id": h.meta.RunID}
	if h.meta.Provider != "" {
		m["provider"] = h.meta.Provider
	}
	if h.meta.Model != "" {
		m["model"] = h.meta.Model
	}
	return m
}

func (h *EinoHandler) write(e Entry) {
	if err := h.store.Append(e); err != nil {
		log.Printf("tape append: %v", err)
	}
}

func (h *EinoHandler) recordMessagesDelta(msgs []*schema.Message) {
	if len(msgs) <= h.recordedMsg {
		return
	}
	for i := h.recordedMsg; i < len(msgs); i++ {
		m := msgs[i]
		if m == nil {
			continue
		}
		if m.Role == schema.Assistant {
			continue
		}
		h.write(Message(messageToMap(m), h.baseMeta()))
	}
	h.recordedMsg = len(msgs)
}

func (h *EinoHandler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	_ = ctx
	_ = info
	switch timing {
	case callbacks.TimingOnStart, callbacks.TimingOnEnd, callbacks.TimingOnError,
		callbacks.TimingOnStartWithStreamInput, callbacks.TimingOnEndWithStreamOutput:
		return true
	default:
		return false
	}
}

func (h *EinoHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if info == nil {
		return ctx
	}
	switch info.Component {
	case components.ComponentOfChatModel:
		mIn := model.ConvCallbackInput(input)
		if mIn == nil {
			return ctx
		}
		h.recordMessagesDelta(mIn.Messages)
	case components.ComponentOfTool:
		tIn := tool.ConvCallbackInput(input)
		if tIn == nil {
			return ctx
		}
		return context.WithValue(ctx, toolCtxKey{}, &pendingTool{Name: info.Name, Args: tIn.ArgumentsInJSON})
	default:
	}
	return ctx
}

func (h *EinoHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if info == nil {
		return ctx
	}
	switch info.Component {
	case components.ComponentOfChatModel:
		mOut := model.ConvCallbackOutput(output)
		if mOut == nil || mOut.Message == nil {
			return ctx
		}
		h.write(Message(messageToMap(mOut.Message), h.baseMeta()))
		runData := map[string]any{"status": "ok"}
		if mOut.TokenUsage != nil {
			runData["usage"] = map[string]any{
				"prompt_tokens":     mOut.TokenUsage.PromptTokens,
				"completion_tokens": mOut.TokenUsage.CompletionTokens,
				"total_tokens":      mOut.TokenUsage.TotalTokens,
			}
		}
		if h.meta.Provider != "" {
			runData["provider"] = h.meta.Provider
		}
		if h.meta.Model != "" {
			runData["model"] = h.meta.Model
		}
		h.write(Event("run", runData, h.baseMeta()))
	case components.ComponentOfTool:
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
				if b, err := sonic.MarshalString(tOut.ToolOutput); err == nil {
					res = b
				}
			}
		}
		h.write(ToolCall([]map[string]any{{
			"name":      name,
			"arguments": args,
		}}, h.baseMeta()))
		h.write(ToolResult([]any{res}, h.baseMeta()))
	default:
	}
	return ctx
}

func (h *EinoHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if info == nil || err == nil {
		return ctx
	}
	ex := map[string]any{"component": string(info.Component), "node": info.Name}
	h.write(ErrorPayload(err.Error(), ex, h.baseMeta()))
	if info.Component == components.ComponentOfChatModel {
		h.write(Event("run", map[string]any{
			"status":   "error",
			"provider": h.meta.Provider,
			"model":    h.meta.Model,
		}, h.baseMeta()))
	}
	return ctx
}

func (h *EinoHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if info == nil || input == nil {
		return ctx
	}
	if info.Component != components.ComponentOfChatModel {
		return ctx
	}
	go func() {
		defer func() {
			if e := recover(); e != nil {
				log.Printf("tape stream input panic: %v\n%s", e, string(debug.Stack()))
			}
			input.Close()
		}()
		var chunks []callbacks.CallbackInput
		for {
			ch, err := input.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				log.Printf("tape stream input recv: %v", err)
				return
			}
			chunks = append(chunks, ch)
		}
		ins := convModelCallbackInputs(chunks)
		_, msgs, _, err := extractModelInput(ins)
		if err != nil {
			log.Printf("tape extract model input: %v", err)
			return
		}
		h.recordMessagesDelta(msgs)
	}()
	return ctx
}

func (h *EinoHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if info == nil || output == nil {
		return ctx
	}
	if info.Component != components.ComponentOfChatModel {
		return ctx
	}
	go func() {
		defer func() {
			if e := recover(); e != nil {
				log.Printf("tape stream output panic: %v\n%s", e, string(debug.Stack()))
			}
			output.Close()
		}()
		var chunks []callbacks.CallbackOutput
		var streamErr error
		for {
			ch, err := output.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				log.Printf("tape stream output recv: %v", err)
				streamErr = err
				break
			}
			chunks = append(chunks, ch)
		}
		outs := convModelCallbackOutputs(chunks)
		usage, msg, _, err := extractModelOutput(outs)
		if err != nil {
			log.Printf("tape extract model output: %v", err)
			return
		}
		if msg != nil {
			h.write(Message(messageToMap(msg), h.baseMeta()))
		}
		status := "ok"
		if streamErr != nil {
			status = "error"
		}
		runData := map[string]any{"status": status}
		if usage != nil {
			runData["usage"] = map[string]any{
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
				"total_tokens":      usage.TotalTokens,
			}
		}
		if h.meta.Provider != "" {
			runData["provider"] = h.meta.Provider
		}
		if h.meta.Model != "" {
			runData["model"] = h.meta.Model
		}
		h.write(Event("run", runData, h.baseMeta()))
	}()
	return ctx
}

func messageToMap(m *schema.Message) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	raw, err := sonic.Marshal(m)
	if err != nil {
		return map[string]any{"role": string(m.Role), "content": m.Content}
	}
	var out map[string]any
	if err := sonic.Unmarshal(raw, &out); err != nil {
		return map[string]any{"role": string(m.Role), "content": m.Content}
	}
	return out
}

func convModelCallbackInputs(in []callbacks.CallbackInput) []*model.CallbackInput {
	ret := make([]*model.CallbackInput, len(in))
	for i, c := range in {
		ret[i] = model.ConvCallbackInput(c)
	}
	return ret
}

func convModelCallbackOutputs(out []callbacks.CallbackOutput) []*model.CallbackOutput {
	ret := make([]*model.CallbackOutput, len(out))
	for i, c := range out {
		ret[i] = model.ConvCallbackOutput(c)
	}
	return ret
}

func extractModelInput(ins []*model.CallbackInput) (config *model.Config, messages []*schema.Message, extra map[string]any, err error) {
	var mas [][]*schema.Message
	for _, in := range ins {
		if in == nil {
			continue
		}
		if len(in.Messages) > 0 {
			mas = append(mas, in.Messages)
		}
		if len(in.Extra) > 0 {
			extra = in.Extra
		}
		if in.Config != nil {
			config = in.Config
		}
	}
	if len(mas) == 0 {
		return config, []*schema.Message{}, extra, nil
	}
	messages, err = concatMessageArrays(mas)
	if err != nil {
		return nil, nil, nil, err
	}
	return config, messages, extra, nil
}

func extractModelOutput(outs []*model.CallbackOutput) (usage *model.TokenUsage, message *schema.Message, extra map[string]any, err error) {
	var mas []*schema.Message
	for _, out := range outs {
		if out == nil {
			continue
		}
		if out.TokenUsage != nil {
			usage = out.TokenUsage
		}
		if out.Message != nil {
			mas = append(mas, out.Message)
		}
		if out.Extra != nil {
			extra = out.Extra
		}
	}
	if len(mas) == 0 {
		return usage, &schema.Message{}, extra, nil
	}
	message, err = schema.ConcatMessages(mas)
	if err != nil {
		return nil, nil, nil, err
	}
	return usage, message, extra, nil
}

func concatMessageArrays(mas [][]*schema.Message) ([]*schema.Message, error) {
	if len(mas) == 0 {
		return nil, nil
	}
	arrayLen := len(mas[0])
	ret := make([]*schema.Message, arrayLen)
	slicesToConcat := make([][]*schema.Message, arrayLen)
	for _, ma := range mas {
		if len(ma) != arrayLen {
			return nil, goerrors.Errorf("mismatch streamed message batch length: got %d want %d", len(ma), arrayLen)
		}
		for i := 0; i < arrayLen; i++ {
			if ma[i] != nil {
				slicesToConcat[i] = append(slicesToConcat[i], ma[i])
			}
		}
	}
	for i, slice := range slicesToConcat {
		switch len(slice) {
		case 0:
			ret[i] = nil
		case 1:
			ret[i] = slice[0]
		default:
			cm, err := schema.ConcatMessages(slice)
			if err != nil {
				return nil, err
			}
			ret[i] = cm
		}
	}
	return ret, nil
}
