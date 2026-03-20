package tape

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// OTLP JSON structures following the OpenTelemetry Protocol specification.

// OTLPTrace is the top-level OTLP trace export structure.
type OTLPTrace struct {
	ResourceSpans []OTLPResourceSpan `json:"resourceSpans"`
}

// OTLPResourceSpan groups spans by resource.
type OTLPResourceSpan struct {
	Resource   OTLPResource    `json:"resource"`
	ScopeSpans []OTLPScopeSpan `json:"scopeSpans"`
}

// OTLPResource describes the entity producing telemetry.
type OTLPResource struct {
	Attributes []OTLPAttribute `json:"attributes"`
}

// OTLPScopeSpan groups spans by instrumentation scope.
type OTLPScopeSpan struct {
	Scope OTLPScope  `json:"scope"`
	Spans []OTLPSpan `json:"spans"`
}

// OTLPScope identifies the instrumentation library.
type OTLPScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// OTLPSpan represents a single operation within a trace.
type OTLPSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []OTLPAttribute `json:"attributes,omitempty"`
	Events            []OTLPEvent     `json:"events,omitempty"`
	Status            OTLPStatus      `json:"status"`
}

// OTLPAttribute is a key-value pair.
type OTLPAttribute struct {
	Key   string             `json:"key"`
	Value OTLPAttributeValue `json:"value"`
}

// OTLPAttributeValue holds the attribute value.
type OTLPAttributeValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"`
}

// OTLPEvent is a time-stamped annotation on a span.
type OTLPEvent struct {
	Name         string          `json:"name"`
	TimeUnixNano string          `json:"timeUnixNano"`
	Attributes   []OTLPAttribute `json:"attributes,omitempty"`
}

// OTLPStatus represents the span outcome.
type OTLPStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

const (
	otelStatusOK         = 1
	otelStatusError      = 2
	otelSpanKindInternal = 1
)

func otelStrAttr(key, value string) OTLPAttribute {
	return OTLPAttribute{Key: key, Value: OTLPAttributeValue{StringValue: &value}}
}

func otelIntAttr(key string, value int) OTLPAttribute {
	s := strconv.Itoa(value)
	return OTLPAttribute{Key: key, Value: OTLPAttributeValue{IntValue: &s}}
}

// spanBuilder tracks an in-progress span during tape-to-OTLP conversion.
type spanBuilder struct {
	spanID       string
	parentSpanID string
	name         string
	component    string
	startNano    int64
	endNano      int64
	attributes   []OTLPAttribute
	events       []OTLPEvent
	status       OTLPStatus
}

// ConvertTapeToOTLP reads a tape JSONL file and writes an OTLP trace JSON file.
func ConvertTapeToOTLP(tapePath, outputPath string) error {
	entries, err := ReadEntries(tapePath)
	if err != nil {
		return err
	}

	entries = dedupEntries(entries)
	spans := buildSpans(entries)
	spans = fixRootSpans(spans)

	traceID := randomHex(16)
	trace := assembleOTLP(traceID, spans)

	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(outputPath, data, 0o644)
}

// ReadEntries reads all tape entries from a JSONL file.
func ReadEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// dedupEntries removes duplicate entries caused by eino firing callbacks at
// both graph-node level (name="") and component level (name=specific).
// For ChatModel entries with the same kind near the same timestamp, keep the named one.
func dedupEntries(entries []Entry) []Entry {
	remove := make(map[int]bool)

	for i, e := range entries {
		comp, _ := e.Meta["component"].(string)
		name, _ := e.Meta["name"].(string)
		if comp != "ChatModel" {
			continue
		}

		// If this entry has an empty name, check if a named version exists nearby
		if name != "" {
			continue
		}

		ts := parseTimeNano(e.Date)
		for j, other := range entries {
			if j == i {
				continue
			}
			otherComp, _ := other.Meta["component"].(string)
			otherName, _ := other.Meta["name"].(string)
			if otherComp != comp || other.Kind != e.Kind || otherName == "" {
				continue
			}
			otherTS := parseTimeNano(other.Date)
			if abs64(ts-otherTS) < int64(time.Second) {
				remove[i] = true
				break
			}
		}
	}

	result := make([]Entry, 0, len(entries)-len(remove))
	for i, e := range entries {
		if !remove[i] {
			result = append(result, e)
		}
	}
	return result
}

//nolint:gocyclo,cyclop // state machine processing tape entries into spans
func buildSpans(entries []Entry) []*spanBuilder {
	type openSpan struct {
		builder *spanBuilder
	}

	var stack []*openSpan
	var completed []*spanBuilder

	parentSpanID := func() string {
		if len(stack) > 0 {
			return stack[len(stack)-1].builder.spanID
		}
		return ""
	}

	findOnStack := func(comp string) (int, *spanBuilder) {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].builder.component == comp {
				return i, stack[i].builder
			}
		}
		return -1, nil
	}

	popFromStack := func(idx int) *spanBuilder {
		sb := stack[idx].builder
		stack = append(stack[:idx], stack[idx+1:]...)
		return sb
	}

	// ensureChatModelSpan creates a ChatModel span if one isn't already on the stack.
	// This handles the PR 31 tape format where messages arrive without event(start/end).
	ensureChatModelSpan := func(ts int64) *spanBuilder {
		if _, existing := findOnStack("ChatModel"); existing != nil {
			return existing
		}
		sb := &spanBuilder{
			spanID:       randomHex(8),
			parentSpanID: parentSpanID(),
			name:         "ChatModel",
			component:    "ChatModel",
			startNano:    ts,
		}
		stack = append(stack, &openSpan{builder: sb})
		return sb
	}

	for _, e := range entries {
		comp, _ := e.Meta["component"].(string)
		name, _ := e.Meta["name"].(string)
		ts := parseTimeNano(e.Date)

		spanName := comp
		if name != "" && name != comp {
			spanName = comp + "/" + name
		}

		switch e.Kind {
		case "event":
			eventName, _ := e.Payload["name"].(string)
			switch eventName {
			case "start", "tool_start":
				sb := &spanBuilder{
					spanID:       randomHex(8),
					parentSpanID: parentSpanID(),
					name:         spanName,
					component:    comp,
					startNano:    ts,
				}
				stack = append(stack, &openSpan{builder: sb})

			case "end":
				if idx, _ := findOnStack(comp); idx >= 0 {
					sb := popFromStack(idx)
					sb.endNano = ts
					sb.status = OTLPStatus{Code: otelStatusOK}
					completed = append(completed, sb)
				}

			case "run":
				// Close ChatModel span on run event (PR 31 format)
				if idx, existing := findOnStack("ChatModel"); existing != nil {
					existing.endNano = ts
					// Extract token usage from run event data
					if data, ok := e.Payload["data"].(map[string]any); ok {
						if usage, ok := data["usage"].(map[string]any); ok {
							addTokenUsageFromMap(existing, usage)
						}
					}
					existing.status = OTLPStatus{Code: otelStatusOK}
					completed = append(completed, popFromStack(idx))
				}
			}

		case "system":
			chatSpan := ensureChatModelSpan(ts)
			content, _ := e.Payload["content"].(string)
			chatSpan.attributes = append(chatSpan.attributes,
				otelStrAttr("llm.system_prompt", otelTruncate(content, 4000)))

		case "message":
			role, _ := e.Payload["role"].(string)
			content, _ := e.Payload["content"].(string)

			switch role {
			case "system":
				chatSpan := ensureChatModelSpan(ts)
				chatSpan.attributes = append(chatSpan.attributes,
					otelStrAttr("llm.system_prompt", otelTruncate(content, 4000)))
			case "user":
				chatSpan := ensureChatModelSpan(ts)
				chatSpan.attributes = append(chatSpan.attributes,
					otelStrAttr("llm.user_message", otelTruncate(content, 4000)))
			case "assistant":
				if idx, existing := findOnStack("ChatModel"); existing != nil {
					existing.endNano = ts
					existing.attributes = append(existing.attributes,
						otelStrAttr("llm.response", otelTruncate(content, 8000)))
					addTokenUsage(existing, e.Meta)
					existing.status = OTLPStatus{Code: otelStatusOK}
					completed = append(completed, popFromStack(idx))
				}
			}

		case "tool_call":
			calls, _ := e.Payload["calls"].([]any)
			// Determine parent: ChatModel span if on stack, else current stack top
			parentID := parentSpanID()
			if _, chatSpan := findOnStack("ChatModel"); chatSpan != nil {
				parentID = chatSpan.spanID
			}
			for _, c := range calls {
				call, _ := c.(map[string]any)
				fn, _ := call["function"].(string)
				if fn == "" {
					fn, _ = call["name"].(string)
				}
				args, _ := call["args"].(string)
				if args == "" {
					args, _ = call["arguments"].(string)
				}
				toolSpan := &spanBuilder{
					spanID:       randomHex(8),
					parentSpanID: parentID,
					name:         "tool/" + fn,
					component:    "Tool",
					startNano:    ts,
					endNano:      ts,
					status:       OTLPStatus{Code: otelStatusOK},
					attributes: []OTLPAttribute{
						otelStrAttr("tool.function", fn),
						otelStrAttr("tool.arguments", otelTruncate(args, 4000)),
					},
				}
				completed = append(completed, toolSpan)
			}

		case "tool_result":
			// Attach result to the most recently completed tool span
			results, _ := e.Payload["results"].([]any)
			if len(results) > 0 && len(completed) > 0 {
				last := completed[len(completed)-1]
				if last.component == "Tool" {
					res := fmt.Sprintf("%v", results[0])
					last.attributes = append(last.attributes,
						otelStrAttr("tool.result", otelTruncate(res, 4000)))
					last.endNano = ts
				}
			}

		case "error":
			errMsg, _ := e.Payload["message"].(string)
			if idx, _ := findOnStack(comp); idx >= 0 {
				sb := popFromStack(idx)
				sb.endNano = ts
				sb.status = OTLPStatus{Code: otelStatusError, Message: errMsg}
				sb.events = append(sb.events, OTLPEvent{
					Name:         "exception",
					TimeUnixNano: strconv.FormatInt(ts, 10),
					Attributes:   []OTLPAttribute{otelStrAttr("exception.message", errMsg)},
				})
				completed = append(completed, sb)
			}
		}
	}

	// Close any remaining open spans with the last entry's timestamp
	var endTime int64
	if len(entries) > 0 {
		endTime = parseTimeNano(entries[len(entries)-1].Date)
	}
	for _, s := range stack {
		if s.builder.endNano == 0 {
			s.builder.endNano = endTime
		}
		completed = append(completed, s.builder)
	}

	return completed
}

func addTokenUsage(sb *spanBuilder, meta map[string]any) {
	tu, ok := meta["token_usage"].(map[string]any)
	if !ok {
		return
	}
	addTokenUsageFromMap(sb, tu)
}

func addTokenUsageFromMap(sb *spanBuilder, tu map[string]any) {
	// Only add if not already present
	for _, a := range sb.attributes {
		if a.Key == "llm.total_tokens" {
			return
		}
	}
	if pt, ok := tu["prompt_tokens"].(float64); ok {
		sb.attributes = append(sb.attributes, otelIntAttr("llm.prompt_tokens", int(pt)))
	}
	if ct, ok := tu["completion_tokens"].(float64); ok {
		sb.attributes = append(sb.attributes, otelIntAttr("llm.completion_tokens", int(ct)))
	}
	if tt, ok := tu["total_tokens"].(float64); ok {
		sb.attributes = append(sb.attributes, otelIntAttr("llm.total_tokens", int(tt)))
	}
}

// fixRootSpans merges multiple root spans into a single hierarchy.
// Eino's Agent callback fires start/end immediately (a thin wrapper), then
// Chain runs the actual work as a separate root span. This function finds the
// shortest root span, makes it the true root, and re-parents all other root
// spans under it, extending its duration to cover all children.
func fixRootSpans(spans []*spanBuilder) []*spanBuilder {
	var roots []*spanBuilder
	for _, s := range spans {
		if s.parentSpanID == "" {
			roots = append(roots, s)
		}
	}
	if len(roots) <= 1 {
		return spans
	}

	// Pick the first root span (earliest start) as the parent
	var earliest *spanBuilder
	for _, r := range roots {
		if earliest == nil || r.startNano < earliest.startNano {
			earliest = r
		}
	}

	// Re-parent other roots under earliest, extend earliest to cover them
	for _, r := range roots {
		if r == earliest {
			continue
		}
		r.parentSpanID = earliest.spanID
		if r.endNano > earliest.endNano {
			earliest.endNano = r.endNano
		}
		// Propagate error status up
		if r.status.Code == otelStatusError && earliest.status.Code != otelStatusError {
			earliest.status = r.status
		}
	}

	return spans
}

func assembleOTLP(traceID string, spans []*spanBuilder) OTLPTrace {
	otlpSpans := make([]OTLPSpan, 0, len(spans))
	for _, sb := range spans {
		otlpSpans = append(otlpSpans, OTLPSpan{
			TraceID:           traceID,
			SpanID:            sb.spanID,
			ParentSpanID:      sb.parentSpanID,
			Name:              sb.name,
			Kind:              otelSpanKindInternal,
			StartTimeUnixNano: strconv.FormatInt(sb.startNano, 10),
			EndTimeUnixNano:   strconv.FormatInt(sb.endNano, 10),
			Attributes:        sb.attributes,
			Events:            sb.events,
			Status:            sb.status,
		})
	}

	return OTLPTrace{
		ResourceSpans: []OTLPResourceSpan{{
			Resource: OTLPResource{
				Attributes: []OTLPAttribute{
					otelStrAttr("service.name", "lapp"),
				},
			},
			ScopeSpans: []OTLPScopeSpan{{
				Scope: OTLPScope{Name: "lapp/tape"},
				Spans: otlpSpans,
			}},
		}},
	}
}

func parseTimeNano(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}

func randomHex(n int) string {
	b := make([]byte, n)
	//nolint:errcheck // crypto/rand.Read always returns len(b), nil on supported platforms
	rand.Read(b)
	return hex.EncodeToString(b)
}

func otelTruncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
