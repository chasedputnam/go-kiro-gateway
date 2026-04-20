package converter

import (
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func anthropicTestCfg() *config.Config {
	return &config.Config{
		FakeReasoningEnabled:     false,
		FakeReasoningMaxTokens:   4000,
		TruncationRecovery:       false,
		ToolDescriptionMaxLength: 10000,
	}
}

// ---------------------------------------------------------------------------
// extractSystemPrompt
// ---------------------------------------------------------------------------

func TestExtractSystemPrompt_String(t *testing.T) {
	result := extractSystemPrompt("You are a helpful assistant.")
	if result != "You are a helpful assistant." {
		t.Fatalf("expected string system prompt, got %q", result)
	}
}

func TestExtractSystemPrompt_ListOfContentBlocks(t *testing.T) {
	system := []any{
		map[string]any{"type": "text", "text": "You are helpful."},
		map[string]any{"type": "text", "text": "Be concise."},
	}
	result := extractSystemPrompt(system)
	if result != "You are helpful.\nBe concise." {
		t.Fatalf("expected joined text, got %q", result)
	}
}

func TestExtractSystemPrompt_WithCacheControl(t *testing.T) {
	system := []any{
		map[string]any{
			"type":          "text",
			"text":          "You are a helpful assistant.",
			"cache_control": map[string]any{"type": "ephemeral"},
		},
	}
	result := extractSystemPrompt(system)
	if result != "You are a helpful assistant." {
		t.Fatalf("expected text ignoring cache_control, got %q", result)
	}
}

func TestExtractSystemPrompt_Nil(t *testing.T) {
	result := extractSystemPrompt(nil)
	if result != "" {
		t.Fatalf("expected empty string for nil, got %q", result)
	}
}

func TestExtractSystemPrompt_EmptyList(t *testing.T) {
	result := extractSystemPrompt([]any{})
	if result != "" {
		t.Fatalf("expected empty string for empty list, got %q", result)
	}
}

func TestExtractSystemPrompt_MixedBlocks(t *testing.T) {
	system := []any{
		map[string]any{"type": "text", "text": "Hello"},
		map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "..."}},
		map[string]any{"type": "text", "text": "World"},
	}
	result := extractSystemPrompt(system)
	if result != "Hello\nWorld" {
		t.Fatalf("expected only text blocks, got %q", result)
	}
}

func TestExtractSystemPrompt_SingleBlock(t *testing.T) {
	system := []any{
		map[string]any{"type": "text", "text": "Single block"},
	}
	result := extractSystemPrompt(system)
	if result != "Single block" {
		t.Fatalf("expected 'Single block', got %q", result)
	}
}

// ---------------------------------------------------------------------------
// convertAnthropicContentToText
// ---------------------------------------------------------------------------

func TestConvertAnthropicContentToText_String(t *testing.T) {
	result := convertAnthropicContentToText("Hello, World!")
	if result != "Hello, World!" {
		t.Fatalf("expected string content, got %q", result)
	}
}

func TestConvertAnthropicContentToText_ListOfTextBlocks(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "Hello"},
		map[string]any{"type": "text", "text": " World"},
	}
	result := convertAnthropicContentToText(content)
	if result != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", result)
	}
}

func TestConvertAnthropicContentToText_IgnoresNonTextBlocks(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "Hello"},
		map[string]any{"type": "tool_use", "id": "call_123", "name": "test", "input": map[string]any{}},
		map[string]any{"type": "text", "text": " World"},
	}
	result := convertAnthropicContentToText(content)
	if result != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", result)
	}
}

func TestConvertAnthropicContentToText_Nil(t *testing.T) {
	result := convertAnthropicContentToText(nil)
	if result != "" {
		t.Fatalf("expected empty string for nil, got %q", result)
	}
}

func TestConvertAnthropicContentToText_EmptyList(t *testing.T) {
	result := convertAnthropicContentToText([]any{})
	if result != "" {
		t.Fatalf("expected empty string for empty list, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// extractToolUsesFromAnthropicContent
// ---------------------------------------------------------------------------

func TestExtractToolUsesFromAnthropicContent_Basic(t *testing.T) {
	content := []any{
		map[string]any{
			"type":  "tool_use",
			"id":    "call_123",
			"name":  "get_weather",
			"input": map[string]any{"location": "Moscow"},
		},
	}
	result := extractToolUsesFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0]["id"] != "call_123" {
		t.Fatal("wrong id")
	}
	if result[0]["type"] != "function" {
		t.Fatal("expected type 'function'")
	}
	fn := result[0]["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatal("wrong function name")
	}
	args := fn["arguments"].(map[string]any)
	if args["location"] != "Moscow" {
		t.Fatal("wrong arguments")
	}
}

func TestExtractToolUsesFromAnthropicContent_Multiple(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "Let me check"},
		map[string]any{
			"type":  "tool_use",
			"id":    "call_1",
			"name":  "bash",
			"input": map[string]any{"cmd": "ls"},
		},
		map[string]any{
			"type":  "tool_use",
			"id":    "call_2",
			"name":  "read_file",
			"input": map[string]any{"path": "/tmp"},
		},
	}
	result := extractToolUsesFromAnthropicContent(content)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
}

func TestExtractToolUsesFromAnthropicContent_StringContent(t *testing.T) {
	result := extractToolUsesFromAnthropicContent("just a string")
	if result != nil {
		t.Fatal("expected nil for string content")
	}
}

func TestExtractToolUsesFromAnthropicContent_SkipsMissingID(t *testing.T) {
	content := []any{
		map[string]any{
			"type":  "tool_use",
			"name":  "test",
			"input": map[string]any{},
		},
	}
	result := extractToolUsesFromAnthropicContent(content)
	if len(result) != 0 {
		t.Fatal("expected empty result for tool_use without id")
	}
}

func TestExtractToolUsesFromAnthropicContent_SkipsMissingName(t *testing.T) {
	content := []any{
		map[string]any{
			"type":  "tool_use",
			"id":    "call_1",
			"input": map[string]any{},
		},
	}
	result := extractToolUsesFromAnthropicContent(content)
	if len(result) != 0 {
		t.Fatal("expected empty result for tool_use without name")
	}
}

func TestExtractToolUsesFromAnthropicContent_NilInput(t *testing.T) {
	content := []any{
		map[string]any{
			"type": "tool_use",
			"id":   "call_1",
			"name": "test",
			// no input field
		},
	}
	result := extractToolUsesFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	fn := result[0]["function"].(map[string]any)
	args := fn["arguments"].(map[string]any)
	if len(args) != 0 {
		t.Fatal("expected empty map for nil input")
	}
}

// ---------------------------------------------------------------------------
// extractToolResultsFromAnthropicContent
// ---------------------------------------------------------------------------

func TestExtractToolResultsFromAnthropicContent_Basic(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_123",
			"content":     "Result text",
		},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(result))
	}
	if result[0]["type"] != "tool_result" {
		t.Fatal("wrong type")
	}
	if result[0]["tool_use_id"] != "call_123" {
		t.Fatal("wrong tool_use_id")
	}
	if result[0]["content"] != "Result text" {
		t.Fatalf("wrong content: %v", result[0]["content"])
	}
}

func TestExtractToolResultsFromAnthropicContent_Multiple(t *testing.T) {
	content := []any{
		map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "Result 1"},
		map[string]any{"type": "text", "text": "Some text"},
		map[string]any{"type": "tool_result", "tool_use_id": "call_2", "content": "Result 2"},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(result))
	}
	if result[0]["tool_use_id"] != "call_1" {
		t.Fatal("wrong first tool_use_id")
	}
	if result[1]["tool_use_id"] != "call_2" {
		t.Fatal("wrong second tool_use_id")
	}
}

func TestExtractToolResultsFromAnthropicContent_EmptyContent(t *testing.T) {
	content := []any{
		map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": ""},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatal("expected 1 result")
	}
	if result[0]["content"] != "(empty result)" {
		t.Fatalf("expected '(empty result)', got %v", result[0]["content"])
	}
}

func TestExtractToolResultsFromAnthropicContent_NilContent(t *testing.T) {
	content := []any{
		map[string]any{"type": "tool_result", "tool_use_id": "call_1"},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatal("expected 1 result")
	}
	if result[0]["content"] != "(empty result)" {
		t.Fatalf("expected '(empty result)', got %v", result[0]["content"])
	}
}

func TestExtractToolResultsFromAnthropicContent_ListContent(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_1",
			"content":     []any{map[string]any{"type": "text", "text": "List result"}},
		},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 1 {
		t.Fatal("expected 1 result")
	}
	if result[0]["content"] != "List result" {
		t.Fatalf("expected 'List result', got %v", result[0]["content"])
	}
}

func TestExtractToolResultsFromAnthropicContent_SkipsMissingToolUseID(t *testing.T) {
	content := []any{
		map[string]any{"type": "tool_result", "content": "Result without ID"},
	}
	result := extractToolResultsFromAnthropicContent(content)
	if len(result) != 0 {
		t.Fatal("expected empty result for tool_result without tool_use_id")
	}
}

func TestExtractToolResultsFromAnthropicContent_StringContent(t *testing.T) {
	result := extractToolResultsFromAnthropicContent("just a string")
	if result != nil {
		t.Fatal("expected nil for string content")
	}
}

// ---------------------------------------------------------------------------
// extractImagesFromToolResults
// ---------------------------------------------------------------------------

func TestExtractImagesFromToolResults_SingleImage(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_123",
			"content": []any{
				map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": "image/png",
						"data":       "iVBORw0KGgoAAAANSUhEUg==",
					},
				},
			},
		},
	}
	images := extractImagesFromToolResults(content)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("wrong media type: %s", images[0].MediaType)
	}
	if images[0].Data != "iVBORw0KGgoAAAANSUhEUg==" {
		t.Fatal("wrong data")
	}
}

func TestExtractImagesFromToolResults_MultipleImages(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_1",
			"content": []any{
				map[string]any{
					"type":   "image",
					"source": map[string]any{"type": "base64", "media_type": "image/png", "data": "first"},
				},
			},
		},
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_2",
			"content": []any{
				map[string]any{
					"type":   "image",
					"source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": "second"},
				},
			},
		},
	}
	images := extractImagesFromToolResults(content)
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	if images[0].Data != "first" {
		t.Fatal("wrong first image data")
	}
	if images[1].Data != "second" {
		t.Fatal("wrong second image data")
	}
}

func TestExtractImagesFromToolResults_NoImages(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_1",
			"content":     []any{map[string]any{"type": "text", "text": "Just text"}},
		},
	}
	images := extractImagesFromToolResults(content)
	if len(images) != 0 {
		t.Fatal("expected no images")
	}
}

func TestExtractImagesFromToolResults_StringContent(t *testing.T) {
	images := extractImagesFromToolResults("just a string")
	if images != nil {
		t.Fatal("expected nil for string content")
	}
}

func TestExtractImagesFromToolResults_ToolResultWithStringContent(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_1",
			"content":     "String result, not a list",
		},
	}
	images := extractImagesFromToolResults(content)
	if len(images) != 0 {
		t.Fatal("expected no images for string tool_result content")
	}
}

func TestExtractImagesFromToolResults_MixedTextAndImage(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "call_1",
			"content": []any{
				map[string]any{"type": "text", "text": "Screenshot captured"},
				map[string]any{
					"type":   "image",
					"source": map[string]any{"type": "base64", "media_type": "image/png", "data": "screenshot_data"},
				},
			},
		},
	}
	images := extractImagesFromToolResults(content)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Data != "screenshot_data" {
		t.Fatal("wrong image data")
	}
}

// ---------------------------------------------------------------------------
// convertAnthropicMessages integration
// ---------------------------------------------------------------------------

func TestConvertAnthropicMessages_UserTextString(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{Role: "user", Content: "Hello"},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatalf("expected 1 message, got %d", len(unified))
	}
	if unified[0].Role != "user" {
		t.Fatal("wrong role")
	}
	if unified[0].Content != "Hello" {
		t.Fatalf("wrong content: %q", unified[0].Content)
	}
}

func TestConvertAnthropicMessages_UserTextBlocks(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "Hello "},
				map[string]any{"type": "text", "text": "World"},
			},
		},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
	if unified[0].Content != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", unified[0].Content)
	}
}

func TestConvertAnthropicMessages_AssistantWithToolUse(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{
			Role: "assistant",
			Content: []any{
				map[string]any{"type": "text", "text": "Let me check"},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "bash",
					"input": map[string]any{"cmd": "ls"},
				},
			},
		},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
	if unified[0].Role != "assistant" {
		t.Fatal("wrong role")
	}
	if unified[0].Content != "Let me check" {
		t.Fatalf("wrong content: %q", unified[0].Content)
	}
	if len(unified[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(unified[0].ToolCalls))
	}
}

func TestConvertAnthropicMessages_UserWithToolResult(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{
			Role: "user",
			Content: []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_1",
					"content":     "file1.txt\nfile2.txt",
				},
			},
		},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
	if len(unified[0].ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(unified[0].ToolResults))
	}
	tr := unified[0].ToolResults[0]
	if tr["tool_use_id"] != "toolu_1" {
		t.Fatal("wrong tool_use_id")
	}
	if tr["content"] != "file1.txt\nfile2.txt" {
		t.Fatalf("wrong content: %v", tr["content"])
	}
}

func TestConvertAnthropicMessages_UserWithImages(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "Look at this:"},
				map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": "image/jpeg",
						"data":       "jpegdata",
					},
				},
			},
		},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
	if unified[0].Content != "Look at this:" {
		t.Fatalf("wrong content: %q", unified[0].Content)
	}
	if len(unified[0].Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(unified[0].Images))
	}
	if unified[0].Images[0].MediaType != "image/jpeg" {
		t.Fatal("wrong media type")
	}
	if unified[0].Images[0].Data != "jpegdata" {
		t.Fatal("wrong image data")
	}
}

func TestConvertAnthropicMessages_UserWithToolResultImages(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{
			Role: "user",
			Content: []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_mcp",
					"content": []any{
						map[string]any{"type": "text", "text": "Screenshot captured"},
						map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": "image/png",
								"data":       "screenshotdata",
							},
						},
					},
				},
			},
		},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
	// Should have tool result.
	if len(unified[0].ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(unified[0].ToolResults))
	}
	// Should have image extracted from inside tool_result.
	if len(unified[0].Images) != 1 {
		t.Fatalf("expected 1 image from tool_result, got %d", len(unified[0].Images))
	}
	if unified[0].Images[0].Data != "screenshotdata" {
		t.Fatal("wrong image data")
	}
}

func TestConvertAnthropicMessages_FullConversation(t *testing.T) {
	msgs := []models.AnthropicMessage{
		{Role: "user", Content: "List files"},
		{
			Role: "assistant",
			Content: []any{
				map[string]any{"type": "text", "text": "I'll check"},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "bash",
					"input": map[string]any{"cmd": "ls"},
				},
			},
		},
		{
			Role: "user",
			Content: []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_1",
					"content":     "file1.txt",
				},
			},
		},
		{Role: "user", Content: "Thanks"},
	}
	unified := convertAnthropicMessages(msgs)
	if len(unified) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(unified))
	}
	if unified[0].Role != "user" || unified[0].Content != "List files" {
		t.Fatal("wrong first message")
	}
	if unified[1].Role != "assistant" || len(unified[1].ToolCalls) != 1 {
		t.Fatal("wrong assistant message")
	}
	if unified[2].Role != "user" || len(unified[2].ToolResults) != 1 {
		t.Fatal("wrong tool result message")
	}
	if unified[3].Role != "user" || unified[3].Content != "Thanks" {
		t.Fatal("wrong last message")
	}
}

// ---------------------------------------------------------------------------
// convertAnthropicTools
// ---------------------------------------------------------------------------

func TestConvertAnthropicTools_Basic(t *testing.T) {
	tools := []models.AnthropicTool{
		{
			Name:        "get_weather",
			Description: "Get weather for a city",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		},
	}
	result := convertAnthropicTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Name != "get_weather" {
		t.Fatal("wrong name")
	}
	if result[0].Description != "Get weather for a city" {
		t.Fatal("wrong description")
	}
	if result[0].InputSchema["type"] != "object" {
		t.Fatal("wrong schema")
	}
}

func TestConvertAnthropicTools_NilSchema(t *testing.T) {
	tools := []models.AnthropicTool{
		{Name: "no_params", Description: "No parameters"},
	}
	result := convertAnthropicTools(tools)
	if len(result) != 1 {
		t.Fatal("expected 1 tool")
	}
	if result[0].InputSchema == nil {
		t.Fatal("expected empty map for nil schema, not nil")
	}
}

func TestConvertAnthropicTools_Empty(t *testing.T) {
	result := convertAnthropicTools(nil)
	if result != nil {
		t.Fatal("expected nil for empty tools")
	}
}

func TestConvertAnthropicTools_Multiple(t *testing.T) {
	tools := []models.AnthropicTool{
		{Name: "tool1", Description: "First", InputSchema: map[string]any{"type": "object"}},
		{Name: "tool2", Description: "Second", InputSchema: map[string]any{"type": "object"}},
	}
	result := convertAnthropicTools(tools)
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// ConvertAnthropicRequest integration
// ---------------------------------------------------------------------------

func TestConvertAnthropicRequest_Basic(t *testing.T) {
	cfg := anthropicTestCfg()
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		System:    "Be helpful",
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	result, err := ConvertAnthropicRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SystemPrompt != "Be helpful" {
		t.Fatalf("wrong system prompt: %q", result.SystemPrompt)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Tools != nil {
		t.Fatal("expected nil tools")
	}
}

func TestConvertAnthropicRequest_WithTools(t *testing.T) {
	cfg := anthropicTestCfg()
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "Check weather"},
		},
		Tools: []models.AnthropicTool{
			{
				Name:        "get_weather",
				Description: "Get weather",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	result, err := ConvertAnthropicRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
}

func TestConvertAnthropicRequest_SystemAsList(t *testing.T) {
	cfg := anthropicTestCfg()
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		System: []any{
			map[string]any{"type": "text", "text": "Part 1"},
			map[string]any{"type": "text", "text": "Part 2", "cache_control": map[string]any{"type": "ephemeral"}},
		},
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	result, err := ConvertAnthropicRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SystemPrompt != "Part 1\nPart 2" {
		t.Fatalf("wrong system prompt: %q", result.SystemPrompt)
	}
}

// ---------------------------------------------------------------------------
// BuildAnthropicKiroPayload integration
// ---------------------------------------------------------------------------

func TestBuildAnthropicKiroPayload_Basic(t *testing.T) {
	cfg := anthropicTestCfg()
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		System:    "You are helpful.",
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "How are you?"},
		},
	}

	result, err := BuildAnthropicKiroPayload(req, "conv-123", "arn:aws:test", "claude-sonnet-4", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	if convState["conversationId"] != "conv-123" {
		t.Fatal("wrong conversationId")
	}

	// History should have the first user + assistant.
	history := convState["history"].([]map[string]any)
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// System prompt should be prepended to first user message.
	firstUser := history[0]["userInputMessage"].(map[string]any)
	content := firstUser["content"].(string)
	if !strings.HasPrefix(content, "You are helpful.") {
		t.Fatal("expected system prompt prepended to first user message")
	}

	// Current message should be the last user message.
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	if userInput["content"] != "How are you?" {
		t.Fatalf("expected 'How are you?', got %v", userInput["content"])
	}
}

func TestBuildAnthropicKiroPayload_WithToolConversation(t *testing.T) {
	cfg := anthropicTestCfg()
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "List files"},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "text", "text": "I'll check"},
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "bash",
						"input": map[string]any{"cmd": "ls"},
					},
				},
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_1",
						"content":     "file1.txt",
					},
				},
			},
			{Role: "user", Content: "Thanks"},
		},
		Tools: []models.AnthropicTool{
			{Name: "bash", Description: "Run bash", InputSchema: map[string]any{"type": "object"}},
		},
	}

	result, err := BuildAnthropicKiroPayload(req, "conv-456", "", "claude-sonnet-4", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)

	// Verify the payload built successfully and the current message exists.
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	if userInput["content"] == nil || userInput["content"] == "" {
		t.Fatal("expected non-empty current message content")
	}

	// Verify history exists.
	history, ok := convState["history"].([]map[string]any)
	if !ok || len(history) == 0 {
		t.Fatal("expected non-empty history")
	}
}

func TestBuildAnthropicKiroPayload_ToolNameValidation(t *testing.T) {
	cfg := anthropicTestCfg()
	longName := strings.Repeat("a", 70)
	req := models.AnthropicMessagesRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 4096,
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: "test"},
		},
		Tools: []models.AnthropicTool{
			{Name: longName, Description: "test", InputSchema: map[string]any{}},
		},
	}

	_, err := BuildAnthropicKiroPayload(req, "conv-1", "", "claude-sonnet-4", cfg)
	if err == nil {
		t.Fatal("expected error for tool name exceeding 64 characters")
	}
	if !strings.Contains(err.Error(), "64 characters") {
		t.Fatalf("error should mention 64 characters: %v", err)
	}
}
