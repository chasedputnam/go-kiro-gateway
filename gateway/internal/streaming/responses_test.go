package streaming

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// defaultResponsesOpts returns minimal Responses API stream options for tests.
func defaultResponsesOpts() ResponsesStreamOptions {
	return ResponsesStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
		MaxInputTokens:       200000,
		InputTokens:          100,
	}
}

// parseResponsesSSE splits raw SSE output into events: type → data map.
// Returns a slice of {eventType, data} pairs in order.
type sseEvent struct {
	EventType string
	Data      map[string]any
}

func parseResponsesSSE(body string) []sseEvent {
	var events []sseEvent
	scanner := bufio.NewScanner(strings.NewReader(body))

	var currentType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			var obj map[string]any
			if err := json.Unmarshal([]byte(dataStr), &obj); err == nil {
				events = append(events, sseEvent{EventType: currentType, Data: obj})
			}
			currentType = ""
		}
	}
	return events
}

// eventsByType returns all SSE events of the given type from the slice.
func eventsByType(events []sseEvent, eventType string) []sseEvent {
	var out []sseEvent
	for _, e := range events {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 8.1 Non-streaming response builder — BuildResponsesResponse
// ---------------------------------------------------------------------------

func TestBuildResponsesResponse_TextOnly(t *testing.T) {
	resp := &CollectedResponse{Content: "Hello, world!"}
	opts := ResponsesNonStreamOptions{Model: "claude-sonnet-4", InputTokens: 50}

	result := BuildResponsesResponse(resp, opts)

	if result.Object != "response" {
		t.Errorf("Object = %q, want response", result.Object)
	}
	if !strings.HasPrefix(result.ID, "resp_") {
		t.Errorf("ID = %q, want resp_ prefix", result.ID)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q", result.Model)
	}
	if result.CreatedAt == 0 {
		t.Error("CreatedAt should be non-zero")
	}

	// One message output item.
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(result.Output))
	}
	item := result.Output[0]
	if item.Type != "message" {
		t.Errorf("output[0].Type = %q, want message", item.Type)
	}
	if item.Role != "assistant" {
		t.Errorf("output[0].Role = %q, want assistant", item.Role)
	}
	if len(item.Content) != 1 || item.Content[0].Text != "Hello, world!" {
		t.Errorf("output[0].Content = %+v", item.Content)
	}

	// Usage.
	if result.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	if result.Usage.InputTokens != 50 {
		t.Errorf("InputTokens = %d, want 50", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens == 0 {
		t.Error("OutputTokens should be > 0 for non-empty content")
	}
	if result.Usage.TotalTokens != result.Usage.InputTokens+result.Usage.OutputTokens {
		t.Errorf("TotalTokens mismatch: %d != %d + %d", result.Usage.TotalTokens, result.Usage.InputTokens, result.Usage.OutputTokens)
	}
}

func TestBuildResponsesResponse_ToolCallsOnly(t *testing.T) {
	resp := &CollectedResponse{
		ToolCalls: []ToolCallInfo{
			{ID: "call_abc", Name: "get_weather", Arguments: `{"location":"London"}`},
		},
	}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{Model: "m"})

	// No text content → no message item; only function_call items.
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(result.Output))
	}
	item := result.Output[0]
	if item.Type != "function_call" {
		t.Errorf("output[0].Type = %q, want function_call", item.Type)
	}
	if item.Name != "get_weather" {
		t.Errorf("Name = %q", item.Name)
	}
	if item.Arguments != `{"location":"London"}` {
		t.Errorf("Arguments = %q", item.Arguments)
	}
	if !strings.HasPrefix(item.ID, "fc_") {
		t.Errorf("ID prefix = %q, want fc_", item.ID)
	}
}

func TestBuildResponsesResponse_CustomToolCall(t *testing.T) {
	resp := &CollectedResponse{
		ToolCalls: []ToolCallInfo{
			{ID: "call_exec", Name: "exec", Arguments: `{"input":"return 2 + 2"}`},
		},
	}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{
		Model:           "m",
		CustomToolNames: map[string]bool{"exec": true},
	})

	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(result.Output))
	}
	item := result.Output[0]
	if item.Type != "custom_tool_call" {
		t.Errorf("Type = %q, want custom_tool_call", item.Type)
	}
	if item.Input != "return 2 + 2" {
		t.Errorf("Input = %q, want raw custom input", item.Input)
	}
	if item.Arguments != "" {
		t.Errorf("Arguments = %q, want empty for custom tool", item.Arguments)
	}
	if !strings.HasPrefix(item.ID, "ctc_") {
		t.Errorf("ID = %q, want ctc_ prefix", item.ID)
	}
}

func TestBuildResponsesResponse_ThinkingPlusTextPlusToolCalls(t *testing.T) {
	resp := &CollectedResponse{
		ThinkingContent: "I should use the tool",
		Content:         "Let me help",
		ToolCalls:       []ToolCallInfo{{ID: "c1", Name: "tool_a", Arguments: "{}"}},
	}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{Model: "m"})

	// Expected order: reasoning, message, function_call.
	if len(result.Output) != 3 {
		t.Fatalf("expected 3 output items, got %d", len(result.Output))
	}
	if result.Output[0].Type != "reasoning" {
		t.Errorf("output[0].Type = %q, want reasoning", result.Output[0].Type)
	}
	if result.Output[1].Type != "message" {
		t.Errorf("output[1].Type = %q, want message", result.Output[1].Type)
	}
	if result.Output[2].Type != "function_call" {
		t.Errorf("output[2].Type = %q, want function_call", result.Output[2].Type)
	}
}

func TestBuildResponsesResponse_ReasoningItemSummary(t *testing.T) {
	resp := &CollectedResponse{ThinkingContent: "My reasoning here"}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{Model: "m"})

	if len(result.Output) == 0 || result.Output[0].Type != "reasoning" {
		t.Fatalf("expected reasoning output item")
	}
	if !strings.HasPrefix(result.Output[0].ID, "rs_") {
		t.Errorf("reasoning ID prefix = %q, want rs_", result.Output[0].ID)
	}
	if len(result.Output[0].Summary) != 1 {
		t.Fatalf("expected 1 summary block, got %d", len(result.Output[0].Summary))
	}
	summary := result.Output[0].Summary[0]
	if summary.Type != "summary_text" {
		t.Errorf("summary.Type = %q, want summary_text", summary.Type)
	}
	if summary.Text != "My reasoning here" {
		t.Errorf("summary.Text = %q", summary.Text)
	}
}

func TestBuildResponsesResponse_EmptyResponse(t *testing.T) {
	resp := &CollectedResponse{}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{Model: "m"})

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Object != "response" {
		t.Errorf("Object = %q, want response", result.Object)
	}
	// No content, thinking, or tool calls → empty output slice.
	if len(result.Output) != 0 {
		t.Errorf("expected 0 output items for empty response, got %d", len(result.Output))
	}
}

func TestBuildResponsesResponse_UsageFields(t *testing.T) {
	resp := &CollectedResponse{
		Content:                "hi",
		ContextUsagePercentage: 50,
	}
	opts := ResponsesNonStreamOptions{Model: "m", MaxInputTokens: 200000, InputTokens: 0}
	result := BuildResponsesResponse(resp, opts)

	// With 50% context usage and 200k max, input tokens should be calculated.
	if result.Usage.InputTokens == 0 {
		t.Error("InputTokens should be calculated from context usage percentage")
	}
	if result.Usage.TotalTokens != result.Usage.InputTokens+result.Usage.OutputTokens {
		t.Error("TotalTokens != InputTokens + OutputTokens")
	}
}

// ---------------------------------------------------------------------------
// 8.2 Streaming — StreamToResponses: text and tool call events
// ---------------------------------------------------------------------------

func TestStreamToResponses_ResponseCreatedIsFirst(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	if len(parsed) == 0 {
		t.Fatal("no SSE events received")
	}
	if parsed[0].EventType != "response.created" {
		t.Errorf("first event type = %q, want response.created", parsed[0].EventType)
	}
}

func TestStreamToResponses_ResponseCompletedIsLast(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	if len(parsed) == 0 {
		t.Fatal("no SSE events received")
	}
	last := parsed[len(parsed)-1]
	if last.EventType != "response.completed" {
		t.Errorf("last event type = %q, want response.completed", last.EventType)
	}
}

func TestStreamToResponses_TextDeltaEventSequence(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeContent, Content: " world"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	types := make([]string, len(parsed))
	for i, e := range parsed {
		types[i] = e.EventType
	}
	body := strings.Join(types, ",")

	// Must include these event types in order.
	requiredOrder := []string{
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.output_item.done",
		"response.completed",
	}
	lastIdx := -1
	for _, req := range requiredOrder {
		found := false
		for i := lastIdx + 1; i < len(parsed); i++ {
			if parsed[i].EventType == req {
				lastIdx = i
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing event type %q in sequence: %s", req, body)
		}
	}
}

func TestStreamToResponses_OutputTextDeltaContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "foo"},
		KiroEvent{Type: EventTypeContent, Content: "bar"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	deltas := eventsByType(parsed, "response.output_text.delta")
	if len(deltas) != 2 {
		t.Fatalf("expected 2 output_text.delta events, got %d", len(deltas))
	}
	if deltas[0].Data["delta"] != "foo" {
		t.Errorf("first delta = %v, want foo", deltas[0].Data["delta"])
	}
	if deltas[1].Data["delta"] != "bar" {
		t.Errorf("second delta = %v, want bar", deltas[1].Data["delta"])
	}
}

func TestStreamToResponses_ToolCallEventSequence(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeToolCallStart, ToolCall: &ToolCallInfo{ID: "call_1", Name: "get_weather"}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_1", Arguments: `{"loc`}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_1", Arguments: `ation":"London"}`}},
		KiroEvent{Type: EventTypeToolCallStop, ToolCall: &ToolCallInfo{ID: "call_1", Name: "get_weather"}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{ID: "call_1", Name: "get_weather", Arguments: `{"location":"London"}`}},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())

	// Must have these event types.
	requireEventType := func(eventType string) {
		t.Helper()
		for _, e := range parsed {
			if e.EventType == eventType {
				return
			}
		}
		t.Errorf("missing required event type %q", eventType)
	}

	requireEventType("response.output_item.added")
	requireEventType("response.function_call_arguments.delta")
	requireEventType("response.function_call_arguments.done")
	requireEventType("response.output_item.done")
	requireEventType("response.completed")
}

func TestStreamToResponses_CustomToolCallEventSequence(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeToolCallStart, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_exec", Arguments: `{"input":"return `}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_exec", Arguments: `2 + 2"}`}},
		KiroEvent{Type: EventTypeToolCallStop, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec", Arguments: `{"input":"return 2 + 2"}`}},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	opts := defaultResponsesOpts()
	opts.CustomToolNames = map[string]bool{"exec": true}
	StreamToResponses(rec, events, opts)

	parsed := parseResponsesSSE(rec.Body.String())
	if got := eventsByType(parsed, "response.function_call_arguments.delta"); len(got) != 0 {
		t.Fatalf("unexpected function argument events for custom tool: %d", len(got))
	}
	deltas := eventsByType(parsed, "response.custom_tool_call_input.delta")
	if len(deltas) != 1 || deltas[0].Data["delta"] != "return 2 + 2" {
		t.Fatalf("custom input deltas = %#v", deltas)
	}
	done := eventsByType(parsed, "response.custom_tool_call_input.done")
	if len(done) != 1 || done[0].Data["input"] != "return 2 + 2" {
		t.Fatalf("custom input done = %#v", done)
	}

	itemDone := eventsByType(parsed, "response.output_item.done")
	if len(itemDone) != 1 {
		t.Fatalf("expected 1 output_item.done, got %d", len(itemDone))
	}
	item, _ := itemDone[0].Data["item"].(map[string]any)
	if item["type"] != "custom_tool_call" || item["input"] != "return 2 + 2" {
		t.Fatalf("custom output item = %#v", item)
	}
	added := eventsByType(parsed, "response.output_item.added")
	if len(added) != 1 {
		t.Fatalf("expected 1 output_item.added, got %d", len(added))
	}
	addedItem, _ := added[0].Data["item"].(map[string]any)

	completed := eventsByType(parsed, "response.completed")
	if len(completed) != 1 {
		t.Fatalf("expected response.completed")
	}
	response, _ := completed[0].Data["response"].(map[string]any)
	output, _ := response["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("completed output = %#v", output)
	}
	finalItem, _ := output[0].(map[string]any)
	if finalItem["type"] != "custom_tool_call" || finalItem["input"] != "return 2 + 2" {
		t.Fatalf("completed custom item = %#v", finalItem)
	}
	if addedItem["id"] == "" || addedItem["id"] != item["id"] || item["id"] != finalItem["id"] {
		t.Fatalf("custom item IDs are not stable: added=%v done=%v completed=%v", addedItem["id"], item["id"], finalItem["id"])
	}
}

func TestStreamToResponses_DuplicateToolLifecycleIDIsIgnored(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeToolCallStart, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_exec", Arguments: `{"input":"return 2 + 2"}`}},
		KiroEvent{Type: EventTypeToolCallStop, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		// Kiro can repeat the lifecycle for the same ID without another input.
		KiroEvent{Type: EventTypeToolCallStart, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		KiroEvent{Type: EventTypeToolCallStop, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec"}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{ID: "call_exec", Name: "exec", Arguments: `{"input":"return 2 + 2"}`}},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	opts := defaultResponsesOpts()
	opts.CustomToolNames = map[string]bool{"exec": true}
	StreamToResponses(rec, events, opts)

	parsed := parseResponsesSSE(rec.Body.String())
	added := eventsByType(parsed, "response.output_item.added")
	done := eventsByType(parsed, "response.output_item.done")
	if len(added) != 1 || len(done) != 1 {
		t.Fatalf("duplicate lifecycle emitted extra items: added=%d done=%d", len(added), len(done))
	}
	item, _ := done[0].Data["item"].(map[string]any)
	if item["input"] != "return 2 + 2" {
		t.Fatalf("custom input = %v, want complete input", item["input"])
	}
}

func TestStreamToResponses_ToolCallArgumentsReassembled(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeToolCallStart, ToolCall: &ToolCallInfo{ID: "call_x", Name: "fn"}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_x", Arguments: `{"a`}},
		KiroEvent{Type: EventTypeToolCallDelta, ToolCall: &ToolCallInfo{ID: "call_x", Arguments: `":1}`}},
		KiroEvent{Type: EventTypeToolCallStop, ToolCall: &ToolCallInfo{ID: "call_x", Name: "fn"}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{ID: "call_x", Name: "fn", Arguments: `{"a":1}`}},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	doneEvents := eventsByType(parsed, "response.function_call_arguments.done")
	if len(doneEvents) == 0 {
		t.Fatal("expected response.function_call_arguments.done event")
	}
	args := doneEvents[0].Data["arguments"]
	if args != `{"a":1}` {
		t.Errorf("arguments = %v, want {\"a\":1}", args)
	}
}

func TestStreamToResponses_ResponseCompletedHasUsage(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hi"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	completedEvents := eventsByType(parsed, "response.completed")
	if len(completedEvents) == 0 {
		t.Fatal("expected response.completed event")
	}
	respData, ok := completedEvents[0].Data["response"].(map[string]any)
	if !ok {
		t.Fatal("expected response object in response.completed")
	}
	usageData, ok := respData["usage"].(map[string]any)
	if !ok {
		t.Fatal("expected usage in response.completed response")
	}
	// input_tokens comes from opts.InputTokens = 100.
	inputTokens, _ := usageData["input_tokens"].(float64)
	if inputTokens == 0 {
		t.Error("input_tokens should be > 0 in response.completed usage")
	}
}

// ---------------------------------------------------------------------------
// 8.3 Streaming — reasoning, error handling, response.completed
// ---------------------------------------------------------------------------

func TestStreamToResponses_ReasoningBeforeTextInCompleted(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "My reasoning"},
		KiroEvent{Type: EventTypeContent, Content: "My answer"},
		KiroEvent{Type: EventTypeDone},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	completedEvents := eventsByType(parsed, "response.completed")
	if len(completedEvents) == 0 {
		t.Fatal("expected response.completed event")
	}
	respData, _ := completedEvents[0].Data["response"].(map[string]any)
	output, _ := respData["output"].([]any)
	if len(output) < 2 {
		t.Fatalf("expected at least 2 output items in completed response, got %d", len(output))
	}
	// Reasoning item should come first.
	firstItem, _ := output[0].(map[string]any)
	if firstItem["type"] != "reasoning" {
		t.Errorf("output[0].type = %v, want reasoning", firstItem["type"])
	}
	// Text message item second.
	secondItem, _ := output[1].(map[string]any)
	if secondItem["type"] != "message" {
		t.Errorf("output[1].type = %v, want message", secondItem["type"])
	}
}

func TestStreamToResponses_ErrorEventInjectsNoticeAndCompletes(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "partial"},
		KiroEvent{Type: EventTypeError, Error: errors.New("upstream timeout")},
	)
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	body := rec.Body.String()
	parsed := parseResponsesSSE(body)

	// Must still end with response.completed.
	if len(parsed) == 0 {
		t.Fatal("no events")
	}
	last := parsed[len(parsed)-1]
	if last.EventType != "response.completed" {
		t.Errorf("last event after error = %q, want response.completed", last.EventType)
	}
}

func TestStreamToResponses_ResponseCreatedHasModelAndID(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})
	rec := httptest.NewRecorder()
	opts := defaultResponsesOpts()
	opts.Model = "test-model-x"
	StreamToResponses(rec, events, opts)

	parsed := parseResponsesSSE(rec.Body.String())
	if len(parsed) == 0 {
		t.Fatal("no events")
	}
	created := parsed[0]
	if created.EventType != "response.created" {
		t.Fatalf("first event = %q, want response.created", created.EventType)
	}
	respData, _ := created.Data["response"].(map[string]any)
	if respData["model"] != "test-model-x" {
		t.Errorf("model = %v, want test-model-x", respData["model"])
	}
	id, _ := respData["id"].(string)
	if !strings.HasPrefix(id, "resp_") {
		t.Errorf("response id = %q, want resp_ prefix", id)
	}
}

func TestStreamToResponses_EmptyStream_StillEmitsCreatedAndCompleted(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})
	rec := httptest.NewRecorder()
	StreamToResponses(rec, events, defaultResponsesOpts())

	parsed := parseResponsesSSE(rec.Body.String())
	types := make([]string, len(parsed))
	for i, e := range parsed {
		types[i] = e.EventType
	}
	if types[0] != "response.created" {
		t.Errorf("first event = %q, want response.created", types[0])
	}
	if types[len(types)-1] != "response.completed" {
		t.Errorf("last event = %q, want response.completed", types[len(types)-1])
	}
}

// ---------------------------------------------------------------------------
// Helper: ensure models package reference compiles (basic type check)
// ---------------------------------------------------------------------------

func TestBuildResponsesResponse_ReturnsCorrectType(t *testing.T) {
	resp := &CollectedResponse{Content: "hi"}
	result := BuildResponsesResponse(resp, ResponsesNonStreamOptions{Model: "m"})
	var _ *models.ResponsesResponse = result // compile-time type assertion

	if result == nil {
		t.Error("result should not be nil")
	}

	// Verify CreatedAt is recent.
	now := time.Now().Unix()
	if result.CreatedAt < now-5 || result.CreatedAt > now+5 {
		t.Errorf("CreatedAt = %d is not close to current time %d", result.CreatedAt, now)
	}
}

// ---------------------------------------------------------------------------
// Print helper for debugging test output
// ---------------------------------------------------------------------------

func printEvents(t *testing.T, events []sseEvent) {
	t.Helper()
	for i, e := range events {
		b, _ := json.Marshal(e.Data)
		t.Logf("[%d] event=%s data=%s", i, e.EventType, b)
	}
}

// Unused — suppress compiler warning.
var _ = fmt.Sprintf
