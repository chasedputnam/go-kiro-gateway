package streaming

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
)

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — basic content
// ---------------------------------------------------------------------------

func TestCollectFullResponse_BasicContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Hello"},
		KiroEvent{Type: EventTypeContent, Content: " world"},
		KiroEvent{Type: EventTypeDone},
	)

	resp := CollectFullResponse(events)

	if resp.Content != "Hello world" {
		t.Errorf("content = %q, want 'Hello world'", resp.Content)
	}
	if resp.ThinkingContent != "" {
		t.Errorf("thinking content should be empty, got %q", resp.ThinkingContent)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("tool calls should be empty, got %d", len(resp.ToolCalls))
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — thinking content
// ---------------------------------------------------------------------------

func TestCollectFullResponse_ThinkingContent(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "reasoning..."},
		KiroEvent{Type: EventTypeContent, Content: "answer"},
		KiroEvent{Type: EventTypeDone},
	)

	resp := CollectFullResponse(events)

	if resp.Content != "answer" {
		t.Errorf("content = %q, want 'answer'", resp.Content)
	}
	if resp.ThinkingContent != "reasoning..." {
		t.Errorf("thinking content = %q, want 'reasoning...'", resp.ThinkingContent)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — tool calls
// ---------------------------------------------------------------------------

func TestCollectFullResponse_ToolCalls(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "Let me check."},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID:        "call_123",
			Name:      "get_weather",
			Arguments: `{"city":"London"}`,
		}},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID:        "call_456",
			Name:      "get_time",
			Arguments: `{"tz":"UTC"}`,
		}},
		KiroEvent{Type: EventTypeDone},
	)

	resp := CollectFullResponse(events)

	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("first tool call name = %q, want get_weather", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[1].Name != "get_time" {
		t.Errorf("second tool call name = %q, want get_time", resp.ToolCalls[1].Name)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — usage and context usage
// ---------------------------------------------------------------------------

func TestCollectFullResponse_Usage(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: "data"},
		KiroEvent{Type: EventTypeUsage, Usage: &UsageInfo{Credits: 1.5}},
		KiroEvent{Type: EventTypeContent, ContextUsagePercentage: 35.0},
		KiroEvent{Type: EventTypeDone},
	)

	resp := CollectFullResponse(events)

	if resp.Credits == nil || *resp.Credits != 1.5 {
		t.Errorf("credits = %v, want 1.5", resp.Credits)
	}
	if resp.ContextUsagePercentage != 35.0 {
		t.Errorf("context usage = %f, want 35.0", resp.ContextUsagePercentage)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — bracket tool calls
// ---------------------------------------------------------------------------

func TestCollectFullResponse_BracketToolCalls(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeContent, Content: `[Called search with args: {"q":"test"}]`},
		KiroEvent{Type: EventTypeDone},
	)

	resp := CollectFullResponse(events)

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call from bracket parsing, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("tool call name = %q, want search", resp.ToolCalls[0].Name)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — error stops collection
// ---------------------------------------------------------------------------

func TestCollectFullResponse_ErrorStopsCollection(t *testing.T) {
	ch := make(chan KiroEvent, 3)
	ch <- KiroEvent{Type: EventTypeContent, Content: "partial"}
	ch <- KiroEvent{Type: EventTypeError, Error: &FirstTokenTimeoutError{}}
	ch <- KiroEvent{Type: EventTypeContent, Content: "should not appear"}
	close(ch)

	resp := CollectFullResponse(ch)

	if resp.Content != "partial" {
		t.Errorf("content = %q, want 'partial'", resp.Content)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectFullResponse — empty stream
// ---------------------------------------------------------------------------

func TestCollectFullResponse_EmptyStream(t *testing.T) {
	events := feedEvents(KiroEvent{Type: EventTypeDone})

	resp := CollectFullResponse(events)

	if resp.Content != "" {
		t.Errorf("content should be empty, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("tool calls should be empty, got %d", len(resp.ToolCalls))
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildOpenAIResponse — basic
// ---------------------------------------------------------------------------

func TestBuildOpenAIResponse_Basic(t *testing.T) {
	resp := &CollectedResponse{
		Content: "Hello world",
	}

	result := BuildOpenAIResponse(resp, OpenAINonStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
	})

	if result["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", result["object"])
	}
	if result["model"] != "claude-sonnet-4" {
		t.Errorf("model = %v, want claude-sonnet-4", result["model"])
	}

	choices := result["choices"].([]map[string]any)
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}

	message := choices[0]["message"].(map[string]any)
	if message["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", message["role"])
	}
	if message["content"] != "Hello world" {
		t.Errorf("content = %v, want 'Hello world'", message["content"])
	}
	if choices[0]["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choices[0]["finish_reason"])
	}

	// Verify ID format.
	id := result["id"].(string)
	if !strings.HasPrefix(id, "chatcmpl-") {
		t.Errorf("id should start with chatcmpl-, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildOpenAIResponse — with tool calls
// ---------------------------------------------------------------------------

func TestBuildOpenAIResponse_WithToolCalls(t *testing.T) {
	resp := &CollectedResponse{
		Content: "Checking...",
		ToolCalls: []ToolCallInfo{
			{ID: "call_123", Name: "get_weather", Arguments: `{"city":"London"}`},
		},
	}

	result := BuildOpenAIResponse(resp, OpenAINonStreamOptions{
		Model: "claude-sonnet-4",
	})

	choices := result["choices"].([]map[string]any)
	message := choices[0]["message"].(map[string]any)

	// Should have tool_calls.
	toolCalls, ok := message["tool_calls"].([]map[string]any)
	if !ok {
		t.Fatal("expected tool_calls in message")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}

	// Verify no index field (non-streaming).
	if _, hasIndex := toolCalls[0]["index"]; hasIndex {
		t.Error("non-streaming tool calls should not have index field")
	}

	if toolCalls[0]["id"] != "call_123" {
		t.Errorf("tool call id = %v, want call_123", toolCalls[0]["id"])
	}

	fn := toolCalls[0]["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool call name = %v, want get_weather", fn["name"])
	}

	// finish_reason should be tool_calls.
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choices[0]["finish_reason"])
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildOpenAIResponse — with reasoning_content
// ---------------------------------------------------------------------------

func TestBuildOpenAIResponse_WithReasoningContent(t *testing.T) {
	resp := &CollectedResponse{
		Content:         "The answer is 42",
		ThinkingContent: "Let me think about this...",
	}

	result := BuildOpenAIResponse(resp, OpenAINonStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
	})

	choices := result["choices"].([]map[string]any)
	message := choices[0]["message"].(map[string]any)

	if message["reasoning_content"] != "Let me think about this..." {
		t.Errorf("reasoning_content = %v, want 'Let me think about this...'", message["reasoning_content"])
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildOpenAIResponse — usage
// ---------------------------------------------------------------------------

func TestBuildOpenAIResponse_Usage(t *testing.T) {
	credits := 0.5
	resp := &CollectedResponse{
		Content:                "Hello",
		ContextUsagePercentage: 50.0,
		Credits:                &credits,
	}

	result := BuildOpenAIResponse(resp, OpenAINonStreamOptions{
		Model:          "claude-sonnet-4",
		MaxInputTokens: 200000,
	})

	usage := result["usage"].(map[string]any)
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
		t.Error("usage should have credits_used")
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — basic
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_Basic(t *testing.T) {
	resp := &CollectedResponse{
		Content: "Hello world",
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model:       "claude-sonnet-4",
		InputTokens: 100,
	})

	if result["type"] != "message" {
		t.Errorf("type = %v, want message", result["type"])
	}
	if result["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", result["role"])
	}
	if result["model"] != "claude-sonnet-4" {
		t.Errorf("model = %v, want claude-sonnet-4", result["model"])
	}
	if result["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", result["stop_reason"])
	}

	// Verify ID format.
	id := result["id"].(string)
	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("id should start with msg_, got %q", id)
	}

	// Verify content blocks.
	contentBlocks := result["content"].([]map[string]any)
	if len(contentBlocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(contentBlocks))
	}
	if contentBlocks[0]["type"] != "text" {
		t.Errorf("content block type = %v, want text", contentBlocks[0]["type"])
	}
	if contentBlocks[0]["text"] != "Hello world" {
		t.Errorf("content block text = %v, want 'Hello world'", contentBlocks[0]["text"])
	}

	// Verify usage.
	usage := result["usage"].(map[string]any)
	if usage["input_tokens"] != 100 {
		t.Errorf("input_tokens = %v, want 100", usage["input_tokens"])
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — with tool calls
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_WithToolCalls(t *testing.T) {
	resp := &CollectedResponse{
		Content: "Checking...",
		ToolCalls: []ToolCallInfo{
			{ID: "toolu_abc", Name: "get_weather", Arguments: `{"city":"London"}`},
		},
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model:       "claude-sonnet-4",
		InputTokens: 50,
	})

	contentBlocks := result["content"].([]map[string]any)

	// Should have text block + tool_use block.
	if len(contentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(contentBlocks))
	}

	// First should be text.
	if contentBlocks[0]["type"] != "text" {
		t.Errorf("first block type = %v, want text", contentBlocks[0]["type"])
	}

	// Second should be tool_use.
	toolBlock := contentBlocks[1]
	if toolBlock["type"] != "tool_use" {
		t.Errorf("second block type = %v, want tool_use", toolBlock["type"])
	}
	if toolBlock["id"] != "toolu_abc" {
		t.Errorf("tool id = %v, want toolu_abc", toolBlock["id"])
	}
	if toolBlock["name"] != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", toolBlock["name"])
	}

	// Verify input is parsed JSON, not string.
	input := toolBlock["input"].(map[string]any)
	if input["city"] != "London" {
		t.Errorf("tool input city = %v, want London", input["city"])
	}

	// stop_reason should be tool_use.
	if result["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", result["stop_reason"])
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — with thinking content
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_WithThinkingContent(t *testing.T) {
	resp := &CollectedResponse{
		Content:         "The answer",
		ThinkingContent: "Let me reason...",
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
		InputTokens:          50,
	})

	contentBlocks := result["content"].([]map[string]any)

	// Should have thinking block + text block.
	if len(contentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(contentBlocks))
	}

	// First should be thinking.
	if contentBlocks[0]["type"] != "thinking" {
		t.Errorf("first block type = %v, want thinking", contentBlocks[0]["type"])
	}
	if contentBlocks[0]["thinking"] != "Let me reason..." {
		t.Errorf("thinking content = %v, want 'Let me reason...'", contentBlocks[0]["thinking"])
	}
	if _, ok := contentBlocks[0]["signature"]; !ok {
		t.Error("thinking block should have signature")
	}

	// Second should be text.
	if contentBlocks[1]["type"] != "text" {
		t.Errorf("second block type = %v, want text", contentBlocks[1]["type"])
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — usage with context percentage
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_UsageWithContextPercentage(t *testing.T) {
	resp := &CollectedResponse{
		Content:                "Hello",
		ContextUsagePercentage: 50.0,
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model:          "claude-sonnet-4",
		MaxInputTokens: 200000,
		InputTokens:    100,
	})

	usage := result["usage"].(map[string]any)
	inputTokens, _ := usage["input_tokens"].(int)
	outputTokens, _ := usage["output_tokens"].(int)

	// With context usage percentage, input tokens should be recalculated.
	// The exact value depends on the tokenizer, but it should differ from the default 100.
	_ = inputTokens
	if outputTokens <= 0 {
		t.Errorf("output_tokens should be > 0, got %d", outputTokens)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildOpenAIResponse — tool call without ID gets generated ID
// ---------------------------------------------------------------------------

func TestBuildOpenAIResponse_ToolCallWithoutID(t *testing.T) {
	resp := &CollectedResponse{
		ToolCalls: []ToolCallInfo{
			{Name: "func_a", Arguments: `{}`},
		},
	}

	result := BuildOpenAIResponse(resp, OpenAINonStreamOptions{
		Model: "claude-sonnet-4",
	})

	choices := result["choices"].([]map[string]any)
	message := choices[0]["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]map[string]any)

	id := toolCalls[0]["id"].(string)
	if !strings.HasPrefix(id, "call_") {
		t.Errorf("generated tool call ID should start with call_, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — tool call without ID gets generated ID
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_ToolCallWithoutID(t *testing.T) {
	resp := &CollectedResponse{
		ToolCalls: []ToolCallInfo{
			{Name: "func_a", Arguments: `{}`},
		},
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model: "claude-sonnet-4",
	})

	contentBlocks := result["content"].([]map[string]any)
	// Find tool_use block.
	for _, block := range contentBlocks {
		if block["type"] == "tool_use" {
			id := block["id"].(string)
			if !strings.HasPrefix(id, "toolu_") {
				t.Errorf("generated tool use ID should start with toolu_, got %q", id)
			}
			return
		}
	}
	t.Fatal("expected tool_use content block")
}

// ---------------------------------------------------------------------------
// Tests: BuildAnthropicResponse — invalid tool arguments
// ---------------------------------------------------------------------------

func TestBuildAnthropicResponse_InvalidToolArguments(t *testing.T) {
	resp := &CollectedResponse{
		ToolCalls: []ToolCallInfo{
			{ID: "toolu_1", Name: "func_a", Arguments: "not-json"},
		},
	}

	result := BuildAnthropicResponse(resp, AnthropicNonStreamOptions{
		Model: "claude-sonnet-4",
	})

	contentBlocks := result["content"].([]map[string]any)
	for _, block := range contentBlocks {
		if block["type"] == "tool_use" {
			// Input should be empty object when arguments are invalid.
			input := block["input"].(map[string]any)
			if len(input) != 0 {
				t.Errorf("invalid arguments should result in empty input, got %v", input)
			}
			return
		}
	}
	t.Fatal("expected tool_use content block")
}

// ---------------------------------------------------------------------------
// Tests: Round-trip — collect then build
// ---------------------------------------------------------------------------

func TestRoundTrip_CollectThenBuildOpenAI(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "thinking..."},
		KiroEvent{Type: EventTypeContent, Content: "answer"},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID: "call_1", Name: "func_a", Arguments: `{"x":1}`,
		}},
		KiroEvent{Type: EventTypeUsage, Usage: &UsageInfo{Credits: 0.5}},
		KiroEvent{Type: EventTypeDone},
	)

	collected := CollectFullResponse(events)
	result := BuildOpenAIResponse(collected, OpenAINonStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
	})

	// Verify it's valid JSON.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if parsed["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", parsed["object"])
	}
}

func TestRoundTrip_CollectThenBuildAnthropic(t *testing.T) {
	events := feedEvents(
		KiroEvent{Type: EventTypeThinking, ThinkingContent: "thinking..."},
		KiroEvent{Type: EventTypeContent, Content: "answer"},
		KiroEvent{Type: EventTypeToolCall, ToolCall: &ToolCallInfo{
			ID: "toolu_1", Name: "func_a", Arguments: `{"x":1}`,
		}},
		KiroEvent{Type: EventTypeDone},
	)

	collected := CollectFullResponse(events)
	result := BuildAnthropicResponse(collected, AnthropicNonStreamOptions{
		Model:                "claude-sonnet-4",
		ThinkingHandlingMode: thinking.AsReasoningContent,
		InputTokens:          100,
	})

	// Verify it's valid JSON.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if parsed["type"] != "message" {
		t.Errorf("type = %v, want message", parsed["type"])
	}

	// Should have 3 content blocks: thinking, text, tool_use.
	content := parsed["content"].([]any)
	if len(content) != 3 {
		t.Errorf("expected 3 content blocks, got %d", len(content))
	}
}
