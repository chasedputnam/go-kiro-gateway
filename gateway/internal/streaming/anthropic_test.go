package streaming

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseAnthropicSSE splits raw Anthropic SSE output into typed events.
// Each event has an "event_type" (from the event: line) and "data" (parsed JSON).
func parseAnthropicSSE(body string) []anthropicSSEEvent {
	var events []anthropicSSEEvent
	lines := strings.Split(body, "\n")

	var currentEventType string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			currentEventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			var data map[string]any
			if err := json.Unmarshal([]byte(dataStr), &data); err == nil {
				events = append(events, anthropicSSEEvent{
					EventType: currentEventType,
					Data:      data,
				})
			}
			currentEventType = ""
		}
	}
	return events
}

type anthropicSSEEvent struct {
	EventType string
	Data      map[string]any
}

// defaultAnthropicOpts returns minimal Anthropic stream options for testing.
func defaultAnthropicOpts() AnthropicStreamOptions {
	return AnthropicStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
		MaxInputTokens:       200000,
		InputTokens:          100,
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — basic content streaming
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_BasicContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeContent, Content: " world"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	// Verify event sequence.
	eventTypes := make([]string, 0, len(sseEvents))
	for _, e := range sseEvents {
		eventTypes = append(eventTypes, e.EventType)
	}

	// Should have: message_start, content_block_start, content_block_delta,
	// content_block_delta, content_block_stop, message_delta, message_stop
	if eventTypes[0] != "message_start" {
		t.Errorf("first event should be message_start, got %q", eventTypes[0])
	}

	// Find content deltas.
	var contentDeltas []string
	for _, e := range sseEvents {
		if e.EventType == "content_block_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				contentDeltas = append(contentDeltas, delta["text"].(string))
			}
		}
	}
	if len(contentDeltas) != 2 {
		t.Fatalf("expected 2 content deltas, got %d", len(contentDeltas))
	}
	if contentDeltas[0] != "Hello" {
		t.Errorf("first delta = %q, want Hello", contentDeltas[0])
	}
	if contentDeltas[1] != " world" {
		t.Errorf("second delta = %q, want ' world'", contentDeltas[1])
	}

	// Verify message_stop is last.
	lastEvent := sseEvents[len(sseEvents)-1]
	if lastEvent.EventType != "message_stop" {
		t.Errorf("last event should be message_stop, got %q", lastEvent.EventType)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — message_start structure
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_MessageStart(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hi"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	// Find message_start.
	var msgStart map[string]any
	for _, e := range sseEvents {
		if e.EventType == "message_start" {
			msgStart = e.Data
			break
		}
	}
	if msgStart == nil {
		t.Fatal("expected message_start event")
	}

	msg := msgStart["message"].(map[string]any)
	if msg["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", msg["role"])
	}
	if msg["model"] != "claude-sonnet-4" {
		t.Errorf("model = %v, want claude-sonnet-4", msg["model"])
	}
	if msg["type"] != "message" {
		t.Errorf("type = %v, want message", msg["type"])
	}

	usage := msg["usage"].(map[string]any)
	if int(usage["input_tokens"].(float64)) != 100 {
		t.Errorf("input_tokens = %v, want 100", usage["input_tokens"])
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — thinking content blocks
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_ThinkingBlocks(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "reasoning..."},
		KiroEvent{Type: EventTypeContent, Content: "The answer"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	// Find thinking content_block_start.
	var thinkingBlockStart map[string]any
	for _, e := range sseEvents {
		if e.EventType == "content_block_start" {
			cb := e.Data["content_block"].(map[string]any)
			if cb["type"] == "thinking" {
				thinkingBlockStart = e.Data
				break
			}
		}
	}
	if thinkingBlockStart == nil {
		t.Fatal("expected thinking content_block_start")
	}

	// Verify thinking block has signature.
	cb := thinkingBlockStart["content_block"].(map[string]any)
	if _, ok := cb["signature"]; !ok {
		t.Error("thinking block should have signature")
	}

	// Find thinking delta.
	var thinkingDelta string
	for _, e := range sseEvents {
		if e.EventType == "content_block_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["type"] == "thinking_delta" {
				thinkingDelta += delta["thinking"].(string)
			}
		}
	}
	if thinkingDelta != "reasoning..." {
		t.Errorf("thinking delta = %q, want 'reasoning...'", thinkingDelta)
	}

	// Verify text content follows.
	var textDelta string
	for _, e := range sseEvents {
		if e.EventType == "content_block_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				textDelta += delta["text"].(string)
			}
		}
	}
	if textDelta != "The answer" {
		t.Errorf("text delta = %q, want 'The answer'", textDelta)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — tool_use content blocks
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_ToolUseBlocks(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Checking weather."},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID:        "toolu_abc123",
			Name:      "get_weather",
			Arguments: `{"city":"London"}`,
		}},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	// Find tool_use content_block_start.
	var toolBlockStart map[string]any
	for _, e := range sseEvents {
		if e.EventType == "content_block_start" {
			cb := e.Data["content_block"].(map[string]any)
			if cb["type"] == "tool_use" {
				toolBlockStart = e.Data
				break
			}
		}
	}
	if toolBlockStart == nil {
		t.Fatal("expected tool_use content_block_start")
	}

	cb := toolBlockStart["content_block"].(map[string]any)
	if cb["id"] != "toolu_abc123" {
		t.Errorf("tool id = %v, want toolu_abc123", cb["id"])
	}
	if cb["name"] != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", cb["name"])
	}

	// Find input_json_delta.
	var inputJSON string
	for _, e := range sseEvents {
		if e.EventType == "content_block_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				inputJSON += delta["partial_json"].(string)
			}
		}
	}
	if inputJSON == "" {
		t.Fatal("expected input_json_delta")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &parsed); err != nil {
		t.Fatalf("input_json_delta should be valid JSON: %v", err)
	}
	if parsed["city"] != "London" {
		t.Errorf("city = %v, want London", parsed["city"])
	}

	// Verify stop_reason is tool_use.
	for _, e := range sseEvents {
		if e.EventType == "message_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["stop_reason"] != "tool_use" {
				t.Errorf("stop_reason = %v, want tool_use", delta["stop_reason"])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — stop_reason end_turn
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_StopReasonEndTurn(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Done."},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	for _, e := range sseEvents {
		if e.EventType == "message_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["stop_reason"] != "end_turn" {
				t.Errorf("stop_reason = %v, want end_turn", delta["stop_reason"])
			}
			usage := e.Data["usage"].(map[string]any)
			if _, ok := usage["output_tokens"]; !ok {
				t.Error("message_delta should have output_tokens in usage")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — event format
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_EventFormat(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hi"},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	body := rec.Body.String()

	// Verify the raw format contains "event: " and "data: " lines.
	if !strings.Contains(body, "event: message_start") {
		t.Error("body should contain 'event: message_start'")
	}
	if !strings.Contains(body, "event: content_block_start") {
		t.Error("body should contain 'event: content_block_start'")
	}
	if !strings.Contains(body, "event: content_block_delta") {
		t.Error("body should contain 'event: content_block_delta'")
	}
	if !strings.Contains(body, "event: content_block_stop") {
		t.Error("body should contain 'event: content_block_stop'")
	}
	if !strings.Contains(body, "event: message_delta") {
		t.Error("body should contain 'event: message_delta'")
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Error("body should contain 'event: message_stop'")
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — headers
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_SetsHeaders(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// ---------------------------------------------------------------------------
// Tests: StreamToAnthropic — bracket tool calls
// ---------------------------------------------------------------------------

func TestStreamToAnthropic_BracketToolCalls(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: `[Called search with args: {"q":"test"}]`},
		KiroEvent{Type: EventTypeDone},
	)

	rec := httptest.NewRecorder()
	StreamToAnthropic(rec, events, defaultAnthropicOpts())

	sseEvents := parseAnthropicSSE(rec.Body.String())

	// Should find a tool_use content_block_start.
	var foundToolUse bool
	for _, e := range sseEvents {
		if e.EventType == "content_block_start" {
			cb := e.Data["content_block"].(map[string]any)
			if cb["type"] == "tool_use" {
				foundToolUse = true
				if cb["name"] != "search" {
					t.Errorf("tool name = %v, want search", cb["name"])
				}
			}
		}
	}
	if !foundToolUse {
		t.Error("expected bracket tool calls to be parsed and emitted as tool_use blocks")
	}

	// stop_reason should be tool_use.
	for _, e := range sseEvents {
		if e.EventType == "message_delta" {
			delta := e.Data["delta"].(map[string]any)
			if delta["stop_reason"] != "tool_use" {
				t.Errorf("stop_reason = %v, want tool_use", delta["stop_reason"])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: GenerateMessageID / GenerateToolUseID
// ---------------------------------------------------------------------------

func TestGenerateMessageID(t *testing.T) {
	id := GenerateMessageID()
	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("message ID should start with msg_, got %q", id)
	}
}

func TestGenerateToolUseID(t *testing.T) {
	id := GenerateToolUseID()
	if !strings.HasPrefix(id, "toolu_") {
		t.Errorf("tool use ID should start with toolu_, got %q", id)
	}
}
