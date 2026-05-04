package streaming

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// feedEvents creates a channel, sends the given events, and closes it.
func feedEvents(events ...KiroEvent) <-chan KiroEvent {
	ch := make(chan KiroEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

// parseSSEChunks splits raw SSE output into individual data payloads.
// It returns the parsed JSON objects and whether [DONE] was found.
func parseSSEChunks(body string) (chunks []map[string]any, hasDone bool) {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			hasDone = true
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err == nil {
			chunks = append(chunks, obj)
		}
	}
	return
}

// defaultOpenAIOpts returns minimal OpenAI stream options for testing.
func defaultOpenAIOpts() OpenAIStreamOptions {
	return OpenAIStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
		MaxInputTokens:       200000,
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — basic content streaming
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_BasicContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeContent, Content: " world"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	body := rec.Body.String()
	chunks, hasDone := parseSSEChunks(body)

	if !hasDone {
		t.Error("expected [DONE] event")
	}

	// Should have: 2 content chunks + 1 final chunk with finish_reason.
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// Verify first chunk has role and content.
	firstChoices := chunks[0]["choices"].([]any)
	firstDelta := firstChoices[0].(map[string]any)["delta"].(map[string]any)
	if firstDelta["role"] != "assistant" {
		t.Errorf("first chunk should have role=assistant, got %v", firstDelta["role"])
	}
	if firstDelta["content"] != "Hello" {
		t.Errorf("first chunk content = %v, want Hello", firstDelta["content"])
	}

	// Verify second chunk has content but no role.
	secondChoices := chunks[1]["choices"].([]any)
	secondDelta := secondChoices[0].(map[string]any)["delta"].(map[string]any)
	if _, hasRole := secondDelta["role"]; hasRole {
		t.Error("second chunk should not have role")
	}
	if secondDelta["content"] != " world" {
		t.Errorf("second chunk content = %v, want ' world'", secondDelta["content"])
	}

	// Verify final chunk has finish_reason=stop and usage.
	lastChunk := chunks[len(chunks)-1]
	lastChoices := lastChunk["choices"].([]any)
	lastChoice := lastChoices[0].(map[string]any)
	if lastChoice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", lastChoice["finish_reason"])
	}
	if _, hasUsage := lastChunk["usage"]; !hasUsage {
		t.Error("final chunk should have usage")
	}

	// Verify common fields.
	if chunks[0]["object"] != "chat.completion.chunk" {
		t.Errorf("object = %v, want chat.completion.chunk", chunks[0]["object"])
	}
	if chunks[0]["model"] != "claude-sonnet-4" {
		t.Errorf("model = %v, want claude-sonnet-4", chunks[0]["model"])
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — thinking content as reasoning_content
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_ThinkingAsReasoningContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "Let me think..."},
		KiroEvent{Type: EventTypeContent, Content: "The answer is 42"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	chunks, _ := parseSSEChunks(rec.Body.String())

	// First chunk should have reasoning_content.
	firstChoices := chunks[0]["choices"].([]any)
	firstDelta := firstChoices[0].(map[string]any)["delta"].(map[string]any)
	if firstDelta["reasoning_content"] != "Let me think..." {
		t.Errorf("reasoning_content = %v, want 'Let me think...'", firstDelta["reasoning_content"])
	}
	if firstDelta["role"] != "assistant" {
		t.Errorf("first chunk should have role=assistant")
	}

	// Second chunk should have regular content.
	secondChoices := chunks[1]["choices"].([]any)
	secondDelta := secondChoices[0].(map[string]any)["delta"].(map[string]any)
	if secondDelta["content"] != "The answer is 42" {
		t.Errorf("content = %v, want 'The answer is 42'", secondDelta["content"])
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — tool calls
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_ToolCalls(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Let me check the weather."},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID:        "call_abc123",
			Name:      "get_weather",
			Arguments: `{"city":"London"}`,
		}},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	chunks, hasDone := parseSSEChunks(rec.Body.String())
	if !hasDone {
		t.Error("expected [DONE]")
	}

	// Find the tool calls chunk.
	var toolCallsChunk map[string]any
	for _, chunk := range chunks {
		choices := chunk["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		if _, hasTCs := delta["tool_calls"]; hasTCs {
			toolCallsChunk = chunk
			break
		}
	}

	if toolCallsChunk == nil {
		t.Fatal("expected a chunk with tool_calls")
	}

	choices := toolCallsChunk["choices"].([]any)
	delta := choices[0].(map[string]any)["delta"].(map[string]any)
	tcs := delta["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}

	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_abc123" {
		t.Errorf("tool call id = %v, want call_abc123", tc["id"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool call name = %v, want get_weather", fn["name"])
	}
	if fn["arguments"] != `{"city":"London"}` {
		t.Errorf("tool call arguments = %v, want {\"city\":\"London\"}", fn["arguments"])
	}

	// Verify finish_reason is tool_calls.
	lastChunk := chunks[len(chunks)-1]
	lastChoices := lastChunk["choices"].([]any)
	if lastChoices[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason should be tool_calls when tool calls present")
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — tool calls with index field
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_ToolCallsHaveIndex(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID: "call_1", Name: "func_a", Arguments: `{"x":1}`,
		}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID: "call_2", Name: "func_b", Arguments: `{"y":2}`,
		}},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	chunks, _ := parseSSEChunks(rec.Body.String())

	// Find tool calls chunk.
	for _, chunk := range chunks {
		choices := chunk["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		if tcs, ok := delta["tool_calls"]; ok {
			tcList := tcs.([]any)
			if len(tcList) != 2 {
				t.Fatalf("expected 2 tool calls, got %d", len(tcList))
			}
			// Verify index fields.
			for i, tc := range tcList {
				tcMap := tc.(map[string]any)
				idx := int(tcMap["index"].(float64))
				if idx != i {
					t.Errorf("tool call %d has index %d, want %d", i, idx, i)
				}
			}
			return
		}
	}
	t.Fatal("no tool calls chunk found")
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — usage in final chunk
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_UsageInFinalChunk(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeUsage, Usage: &UsageInfo{Credits: 0.5}},
		KiroEvent{Type: EventTypeContent, ContextUsagePercentage: 42.5},
		KiroEvent{Type: EventTypeDone},
	)

	opts := defaultOpenAIOpts()
	opts.MaxInputTokens = 200000

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, opts)

	chunks, _ := parseSSEChunks(rec.Body.String())
	lastChunk := chunks[len(chunks)-1]

	usage, ok := lastChunk["usage"].(map[string]any)
	if !ok {
		t.Fatal("final chunk should have usage")
	}

	if _, ok := usage["prompt_tokens"]; !ok {
		t.Error("usage should have prompt_tokens")
	}
	if _, ok := usage["completion_tokens"]; !ok {
		t.Error("usage should have completion_tokens")
	}
	if _, ok := usage["total_tokens"]; !ok {
		t.Error("usage should have total_tokens")
	}
	if _, ok := usage["credits_used"]; !ok {
		t.Error("usage should have credits_used when metering data present")
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — empty stream
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_EmptyStream(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	chunks, hasDone := parseSSEChunks(rec.Body.String())
	if !hasDone {
		t.Error("expected [DONE]")
	}

	// Should have at least the final chunk with finish_reason.
	if len(chunks) < 1 {
		t.Fatal("expected at least 1 chunk")
	}

	lastChunk := chunks[len(chunks)-1]
	lastChoices := lastChunk["choices"].([]any)
	if lastChoices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason should be stop for empty stream")
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — Content-Type header
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_SetsHeaders(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — non-flusher response writer
// ---------------------------------------------------------------------------

type nonFlusherWriter struct {
	http.ResponseWriter
}

func TestStreamToOpenAI_NonFlusherWriter(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})

	rec := httptest.NewRecorder()
	nfw := &nonFlusherWriter{ResponseWriter: rec}

	StreamToOpenAI(nfw, events, defaultOpenAIOpts())

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for non-flusher writer, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — error event stops streaming
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_ErrorEventStopsStreaming(t *testing.T) {
	ch := make(chan KiroEvent, 3)
	ch <- KiroEvent{Type: EventTypeContent, Content: "partial"}
	ch <- KiroEvent{Type: EventTypeError, Error: &FirstTokenTimeoutError{}}
	ch <- KiroEvent{Type: EventTypeContent, Content: "should not appear"}
	close(ch)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, ch, defaultOpenAIOpts())

	body := rec.Body.String()
	// On error, the stream must still be terminated cleanly with a final
	// chunk (finish_reason=stop) and [DONE] so OpenAI SDK clients don't
	// hang waiting for stream termination.
	if !strings.Contains(body, "[DONE]") {
		t.Error("expected [DONE] even after error — client needs clean stream termination")
	}
	// A short error notice must be injected into the stream so the agent
	// knows the response was truncated due to a gateway error.
	if !strings.Contains(body, "[Gateway error:") {
		t.Error("expected gateway error notice in stream content")
	}
	if strings.Contains(body, "should not appear") {
		t.Error("content after error should not appear")
	}
	// Verify the partial content before the error was sent.
	if !strings.Contains(body, "partial") {
		t.Error("partial content before error should have been sent")
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToOpenAI — bracket tool calls from content
// ---------------------------------------------------------------------------

func TestStreamToOpenAI_BracketToolCalls(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: `[Called get_weather with args: {"city":"Paris"}]`},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToOpenAI(rec, events, defaultOpenAIOpts())

	chunks, _ := parseSSEChunks(rec.Body.String())

	// Should find a tool calls chunk.
	var foundToolCalls bool
	for _, chunk := range chunks {
		choices := chunk["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		if _, ok := delta["tool_calls"]; ok {
			foundToolCalls = true
			tcs := delta["tool_calls"].([]any)
			tc := tcs[0].(map[string]any)
			fn := tc["function"].(map[string]any)
			if fn["name"] != "get_weather" {
				t.Errorf("bracket tool call name = %v, want get_weather", fn["name"])
			}
		}
	}
	if !foundToolCalls {
		t.Error("expected bracket tool calls to be parsed and emitted")
	}

	// finish_reason should be tool_calls.
	lastChunk := chunks[len(chunks)-1]
	lastChoices := lastChunk["choices"].([]any)
	if lastChoices[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Error("finish_reason should be tool_calls when bracket tool calls present")
	}
}

// ---------------------------------------------------------------------------
// Tests: GenerateCompletionID / GenerateToolCallID
// ---------------------------------------------------------------------------

func TestGenerateCompletionID(t *testing.T) {
	id := GenerateCompletionID()
	if !strings.HasPrefix(id, "chatcmpl-") {
		t.Errorf("completion ID should start with chatcmpl-, got %q", id)
	}
}

func TestGenerateToolCallID(t *testing.T) {
	id := GenerateToolCallID()
	if !strings.HasPrefix(id, "call_") {
		t.Errorf("tool call ID should start with call_, got %q", id)
	}
}
