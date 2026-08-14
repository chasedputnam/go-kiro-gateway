package converter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// inputItems marshals a []models.InputItem to []any so tests exercise the
// same JSON-round-trip code path that the real HTTP handler uses.
func inputItems(items []models.InputItem) []any {
	b, err := json.Marshal(items)
	if err != nil {
		panic(err)
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

// ---------------------------------------------------------------------------
// 5.1 Input variants
// ---------------------------------------------------------------------------

func TestConvertResponsesRequest_StringInput(t *testing.T) {
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: "Hello, world!",
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.Role != "user" {
		t.Errorf("role = %q, want %q", msg.Role, "user")
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("content = %q, want %q", msg.Content, "Hello, world!")
	}
}

func TestConvertResponsesRequest_EmptyStringInput(t *testing.T) {
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: "",
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages for empty string input, got %d", len(result.Messages))
	}
}

func TestConvertResponsesRequest_NilInput(t *testing.T) {
	req := models.ResponsesRequest{Model: "claude-sonnet-4", Input: nil}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages for nil input, got %d", len(result.Messages))
	}
}

func TestConvertResponsesRequest_ArrayUserMessage(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "Hello from user"},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", result.Messages[0].Role)
	}
	if result.Messages[0].Content != "Hello from user" {
		t.Errorf("content = %q, want 'Hello from user'", result.Messages[0].Content)
	}
}

func TestConvertResponsesRequest_ArrayAssistantMessage(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "message", Role: "assistant", Content: "Hello there!"},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Role != "assistant" {
		t.Errorf("second message role = %q, want assistant", result.Messages[1].Role)
	}
	if result.Messages[1].Content != "Hello there!" {
		t.Errorf("second message content = %q, want 'Hello there!'", result.Messages[1].Content)
	}
}

func TestConvertResponsesRequest_FunctionCallItem(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "What's the weather?"},
		{Type: "function_call", ID: "fc_abc", CallID: "call_123", Name: "get_weather", Arguments: `{"location":"London"}`},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	assistant := result.Messages[1]
	if assistant.Role != "assistant" {
		t.Errorf("role = %q, want assistant", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	fn, _ := tc["function"].(map[string]any)
	if fn == nil {
		t.Fatal("expected function field in tool call")
	}
	if fn["name"] != "get_weather" {
		t.Errorf("tool call name = %v, want get_weather", fn["name"])
	}
	if tc["id"] != "call_123" {
		t.Errorf("tool call id = %v, want call_123", tc["id"])
	}
}

func TestConvertResponsesRequest_FunctionCallOutputItem(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "What's the weather?"},
		{Type: "function_call", ID: "fc_abc", CallID: "call_123", Name: "get_weather", Arguments: `{"location":"London"}`},
		{Type: "function_call_output", CallID: "call_123", Output: "Sunny, 22°C"},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: user msg, assistant tool call, user tool result flush
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}
	toolResultMsg := result.Messages[2]
	if toolResultMsg.Role != "user" {
		t.Errorf("tool result message role = %q, want user", toolResultMsg.Role)
	}
	if len(toolResultMsg.ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(toolResultMsg.ToolResults))
	}
	tr := toolResultMsg.ToolResults[0]
	if tr["tool_use_id"] != "call_123" {
		t.Errorf("tool_use_id = %v, want call_123", tr["tool_use_id"])
	}
	if tr["content"] != "Sunny, 22°C" {
		t.Errorf("content = %v, want 'Sunny, 22°C'", tr["content"])
	}
}

func TestConvertResponsesRequest_ToolResultsFlushBeforeNextMessage(t *testing.T) {
	// Two consecutive function_call_output items should be flushed together
	// into ONE user message with two tool results.
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "check both"},
		{Type: "function_call", ID: "fc1", CallID: "call_1", Name: "tool_a", Arguments: "{}"},
		{Type: "function_call", ID: "fc2", CallID: "call_2", Name: "tool_b", Arguments: "{}"},
		{Type: "function_call_output", CallID: "call_1", Output: "result_a"},
		{Type: "function_call_output", CallID: "call_2", Output: "result_b"},
		{Type: "message", Role: "user", Content: "next turn"},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected: user, assistant(tool_a), assistant(tool_b), user(tool results x2), user(next turn)
	// Note: each function_call is its own assistant message, then two FCOs flush into one user message.
	if len(result.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(result.Messages), result.Messages)
	}
	// The flushed tool results message (index 3) should have 2 tool results.
	toolResultMsg := result.Messages[3]
	if len(toolResultMsg.ToolResults) != 2 {
		t.Errorf("expected 2 tool results flushed together, got %d", len(toolResultMsg.ToolResults))
	}
}

// ---------------------------------------------------------------------------
// 5.1 Instructions as system prompt
// ---------------------------------------------------------------------------

func TestConvertResponsesRequest_InstructionsAsSystemPrompt(t *testing.T) {
	req := models.ResponsesRequest{
		Model:        "claude-sonnet-4",
		Input:        "Hello",
		Instructions: "  You are a helpful assistant.  ",
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("SystemPrompt = %q, want trimmed value", result.SystemPrompt)
	}
}

func TestConvertResponsesRequest_NoInstructions(t *testing.T) {
	req := models.ResponsesRequest{Model: "claude-sonnet-4", Input: "Hi"}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SystemPrompt != "" {
		t.Errorf("SystemPrompt = %q, want empty", result.SystemPrompt)
	}
}

// ---------------------------------------------------------------------------
// 5.2 Content block handling
// ---------------------------------------------------------------------------

func TestConvertResponsesRequest_InputTextContentBlock(t *testing.T) {
	blocks := []models.ContentBlock{
		{Type: "input_text", Text: "What is 2+2?"},
	}
	blocksJSON, _ := json.Marshal(blocks)
	var blocksAny any
	json.Unmarshal(blocksJSON, &blocksAny)

	items := []models.InputItem{
		{Type: "message", Role: "user", Content: blocksAny},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Content != "What is 2+2?" {
		t.Errorf("content = %q, want 'What is 2+2?'", result.Messages[0].Content)
	}
}

func TestConvertResponsesRequest_OutputTextContentBlock(t *testing.T) {
	blocks := []models.ContentBlock{
		{Type: "output_text", Text: "Prior assistant response"},
	}
	blocksJSON, _ := json.Marshal(blocks)
	var blocksAny any
	json.Unmarshal(blocksJSON, &blocksAny)

	items := []models.InputItem{
		{Type: "message", Role: "assistant", Content: blocksAny},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Messages[0].Content != "Prior assistant response" {
		t.Errorf("content = %q", result.Messages[0].Content)
	}
}

func TestConvertResponsesRequest_InputImageContentBlock_DataURL(t *testing.T) {
	blocks := []models.ContentBlock{
		{
			Type: "input_image",
			ImageURL: &models.ImageURLBlock{
				URL:    "data:image/png;base64,iVBORw0KGgo=",
				Detail: "auto",
			},
		},
	}
	blocksJSON, _ := json.Marshal(blocks)
	var blocksAny any
	json.Unmarshal(blocksJSON, &blocksAny)

	items := []models.InputItem{
		{Type: "message", Role: "user", Content: blocksAny},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]
	if len(msg.Images) != 1 {
		t.Fatalf("expected 1 image extracted, got %d", len(msg.Images))
	}
	if msg.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", msg.Images[0].MediaType)
	}
	if msg.Images[0].Data != "iVBORw0KGgo=" {
		t.Errorf("Data = %q, want iVBORw0KGgo=", msg.Images[0].Data)
	}
}

func TestConvertResponsesRequest_MultipleContentBlocks_ConcatenatedText(t *testing.T) {
	blocks := []models.ContentBlock{
		{Type: "input_text", Text: "First"},
		{Type: "input_text", Text: "Second"},
	}
	blocksJSON, _ := json.Marshal(blocks)
	var blocksAny any
	json.Unmarshal(blocksJSON, &blocksAny)

	items := []models.InputItem{
		{Type: "message", Role: "user", Content: blocksAny},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Messages[0].Content != "First\nSecond" {
		t.Errorf("content = %q, want 'First\\nSecond'", result.Messages[0].Content)
	}
}

// ---------------------------------------------------------------------------
// 5.3 Tool conversion and unknown-type skip
// ---------------------------------------------------------------------------

func TestConvertResponsesRequest_ToolConversion(t *testing.T) {
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: "hi",
		Tools: []models.ResponsesTool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get weather for a location",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
					"required": []string{"location"},
				},
			},
		},
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	tool := result.Tools[0]
	if tool.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", tool.Name)
	}
	if tool.Description != "Get weather for a location" {
		t.Errorf("Description = %q", tool.Description)
	}
	if tool.InputSchema == nil {
		t.Error("InputSchema should not be nil")
	}
}

func TestConvertResponsesRequest_AdditionalTools(t *testing.T) {
	items := []models.InputItem{
		{
			Type: "additional_tools",
			Role: "developer",
			Tools: []models.ResponsesTool{
				{
					Type: "namespace",
					Name: "functions",
					Tools: []models.ResponsesTool{
						{Type: "custom", Name: "exec", Description: "Run code"},
						{
							Type:        "function",
							Name:        "wait",
							Description: "Wait for output",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"cell_id": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
		{Type: "message", Role: "user", Content: "inspect the repository"},
	}

	result, err := ConvertResponsesRequest(models.ResponsesRequest{
		Model: "gpt-5.6-terra",
		Input: inputItems(items),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected only the conversational message, got %d", len(result.Messages))
	}
	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 flattened tools, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "exec" || result.Tools[0].Kind != "custom" {
		t.Fatalf("custom tool = %+v, want exec/custom", result.Tools[0])
	}
	if required, ok := result.Tools[0].InputSchema["required"].([]string); !ok || len(required) != 1 || required[0] != "input" {
		t.Fatalf("custom tool schema does not require input: %#v", result.Tools[0].InputSchema)
	}
	if result.Tools[1].Name != "wait" || result.Tools[1].Kind != "" {
		t.Fatalf("function tool = %+v, want wait/function", result.Tools[1])
	}
}

func TestConvertResponsesRequest_CustomToolCallHistory(t *testing.T) {
	items := []models.InputItem{
		{Type: "custom_tool_call", ID: "ctc_1", CallID: "call_1", Name: "exec", Input: "return 2 + 2"},
		{Type: "custom_tool_call_output", CallID: "call_1", Output: "4"},
	}

	result, err := ConvertResponsesRequest(models.ResponsesRequest{
		Model: "gpt-5.6-terra",
		Input: inputItems(items),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected call and result messages, got %d", len(result.Messages))
	}
	call := result.Messages[0].ToolCalls[0]
	if call["id"] != "call_1" {
		t.Fatalf("tool call id = %v, want call_1", call["id"])
	}
	fn := call["function"].(map[string]any)
	if fn["arguments"] != `{"input":"return 2 + 2"}` {
		t.Fatalf("wrapped custom input = %v", fn["arguments"])
	}
	if got := result.Messages[1].ToolResults[0]["tool_use_id"]; got != "call_1" {
		t.Fatalf("tool result id = %v, want call_1", got)
	}
}

func TestConvertResponsesRequest_DeduplicatesCustomCallLifecycleAndArrayOutput(t *testing.T) {
	items := []models.InputItem{
		{Type: "additional_tools", Tools: []models.ResponsesTool{
			{Type: "custom", Name: "exec", Description: "Run code"},
		}},
		{Type: "custom_tool_call", ID: "ctc_done", CallID: "call_1", Name: "exec", Input: "return 2 + 2"},
		{Type: "custom_tool_call", ID: "ctc_added", CallID: "call_1", Name: "exec", Input: ""},
		{Type: "message", Role: "assistant", Content: "I'll inspect that."},
		{Type: "custom_tool_call_output", CallID: "call_1", Output: []any{
			map[string]any{"type": "input_text", "text": "Script completed\n"},
			map[string]any{"type": "input_text", "text": "4"},
		}},
		{Type: "custom_tool_call_output", CallID: "call_1", Output: "duplicate execution error"},
	}

	result, err := ConvertResponsesRequest(models.ResponsesRequest{
		Model: "gpt-5.6-terra",
		Input: inputItems(items),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected call, assistant text, and result messages, got %d", len(result.Messages))
	}
	if len(result.Messages[0].ToolCalls) != 1 {
		t.Fatalf("expected one deduplicated call, got %d", len(result.Messages[0].ToolCalls))
	}
	fn := result.Messages[0].ToolCalls[0]["function"].(map[string]any)
	if fn["arguments"] != `{"input":"return 2 + 2"}` {
		t.Fatalf("deduplicated call arguments = %v", fn["arguments"])
	}
	if len(result.Messages[2].ToolResults) != 1 {
		t.Fatalf("expected one coalesced result, got %d", len(result.Messages[2].ToolResults))
	}
	content, _ := result.Messages[2].ToolResults[0]["content"].(string)
	if !strings.Contains(content, "Script completed\n4") || !strings.Contains(content, "duplicate execution error") {
		t.Fatalf("coalesced result content = %q", content)
	}

	payload, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       result.Messages,
		Tools:          result.Tools,
		ModelID:        "gpt-5.6-terra",
		ConversationID: "conv_1",
		InjectThinking: false,
		Cfg:            testCfg(),
	})
	if err != nil {
		t.Fatalf("build Kiro payload: %v", err)
	}
	state := payload.Payload["conversationState"].(map[string]any)
	history := state["history"].([]map[string]any)
	lastAssistant := history[len(history)-1]["assistantResponseMessage"].(map[string]any)
	uses := lastAssistant["toolUses"].([]map[string]any)
	if len(uses) != 1 || uses[0]["toolUseId"] != "call_1" {
		t.Fatalf("Kiro tool uses = %#v", uses)
	}
	current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	context := current["userInputMessageContext"].(map[string]any)
	results := context["toolResults"].([]map[string]any)
	if len(results) != 1 || results[0]["toolUseId"] != "call_1" {
		t.Fatalf("Kiro tool results = %#v", results)
	}
}

func TestConvertResponsesRequest_EmptyTools(t *testing.T) {
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: "hi",
		Tools: []models.ResponsesTool{},
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tools != nil {
		t.Errorf("expected nil tools for empty slice, got %v", result.Tools)
	}
}

func TestConvertResponsesRequest_NilTools(t *testing.T) {
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: "hi",
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tools != nil {
		t.Errorf("expected nil tools, got %v", result.Tools)
	}
}

func TestConvertResponsesRequest_UnknownInputItemType_Skipped(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "before"},
		{Type: "computer_call_output"}, // unknown type
		{Type: "message", Role: "user", Content: "after"},
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the two message items should appear; unknown type silently skipped.
	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages (unknown type skipped), got %d", len(result.Messages))
	}
	if result.Messages[0].Content != "before" {
		t.Errorf("first message content = %q, want 'before'", result.Messages[0].Content)
	}
	if result.Messages[1].Content != "after" {
		t.Errorf("second message content = %q, want 'after'", result.Messages[1].Content)
	}
}

func TestConvertResponsesRequest_FunctionCallOutputEmptyResult(t *testing.T) {
	items := []models.InputItem{
		{Type: "message", Role: "user", Content: "go"},
		{Type: "function_call", ID: "fc1", CallID: "call_1", Name: "noop", Arguments: "{}"},
		{Type: "function_call_output", CallID: "call_1", Output: ""}, // empty output
	}
	req := models.ResponsesRequest{
		Model: "claude-sonnet-4",
		Input: inputItems(items),
	}
	result, err := ConvertResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have 3 messages: user, assistant(tc), user(tool result)
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}
	toolResultMsg := result.Messages[2]
	tr := toolResultMsg.ToolResults[0]
	// Empty output should be replaced with the "(empty result)" placeholder.
	if tr["content"] != "(empty result)" {
		t.Errorf("content = %v, want '(empty result)'", tr["content"])
	}
}
