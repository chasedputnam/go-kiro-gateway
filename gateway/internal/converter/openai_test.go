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

func openaiTestCfg() *config.Config {
	return &config.Config{
		FakeReasoningEnabled:     false,
		FakeReasoningMaxTokens:   4000,
		TruncationRecovery:       false,
		ToolDescriptionMaxLength: 10000,
	}
}

// ---------------------------------------------------------------------------
// System prompt extraction
// ---------------------------------------------------------------------------

func TestConvertOpenAIMessages_ExtractsSystemPrompt(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Hello"},
	}
	sysPrompt, unified := convertOpenAIMessages(msgs)

	if sysPrompt != "You are helpful.\nBe concise." {
		t.Fatalf("expected combined system prompt, got %q", sysPrompt)
	}
	if len(unified) != 1 {
		t.Fatalf("expected 1 unified message (user only), got %d", len(unified))
	}
	if unified[0].Role != "user" || unified[0].Content != "Hello" {
		t.Fatalf("unexpected message: %+v", unified[0])
	}
}

func TestConvertOpenAIMessages_NoSystemMessages(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "user", Content: "Hi"},
	}
	sysPrompt, unified := convertOpenAIMessages(msgs)

	if sysPrompt != "" {
		t.Fatalf("expected empty system prompt, got %q", sysPrompt)
	}
	if len(unified) != 1 {
		t.Fatal("expected 1 message")
	}
}

// ---------------------------------------------------------------------------
// Tool message conversion
// ---------------------------------------------------------------------------

func TestConvertOpenAIMessages_ToolMessages(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "assistant", Content: "Let me check", ToolCalls: []any{
			map[string]any{
				"id":       "call_1",
				"type":     "function",
				"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		{Role: "tool", Content: "file1.txt\nfile2.txt", ToolCallID: "call_1"},
		{Role: "user", Content: "Thanks"},
	}
	_, unified := convertOpenAIMessages(msgs)

	if len(unified) != 3 {
		t.Fatalf("expected 3 messages (assistant, user-with-tool-results, user), got %d", len(unified))
	}

	// First: assistant with tool calls.
	if unified[0].Role != "assistant" {
		t.Fatal("expected assistant")
	}
	if len(unified[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(unified[0].ToolCalls))
	}

	// Second: synthetic user message with tool results.
	if unified[1].Role != "user" {
		t.Fatal("expected user for tool results")
	}
	if len(unified[1].ToolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(unified[1].ToolResults))
	}
	tr := unified[1].ToolResults[0]
	if tr["tool_use_id"] != "call_1" {
		t.Fatalf("wrong tool_use_id: %v", tr["tool_use_id"])
	}
	if tr["content"] != "file1.txt\nfile2.txt" {
		t.Fatalf("wrong tool result content: %v", tr["content"])
	}

	// Third: regular user message.
	if unified[2].Content != "Thanks" {
		t.Fatal("expected 'Thanks'")
	}
}

func TestConvertOpenAIMessages_ToolMessagesAtEnd(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "assistant", Content: "checking", ToolCalls: []any{
			map[string]any{
				"id":       "call_2",
				"type":     "function",
				"function": map[string]any{"name": "read", "arguments": "{}"},
			},
		}},
		{Role: "tool", Content: "result data", ToolCallID: "call_2"},
	}
	_, unified := convertOpenAIMessages(msgs)

	// Should flush pending tool results at end.
	if len(unified) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(unified))
	}
	if len(unified[1].ToolResults) != 1 {
		t.Fatal("expected tool results flushed at end")
	}
}

func TestConvertOpenAIMessages_EmptyToolContent(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "tool", Content: "", ToolCallID: "call_3"},
		{Role: "user", Content: "ok"},
	}
	_, unified := convertOpenAIMessages(msgs)

	if len(unified) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(unified))
	}
	tr := unified[0].ToolResults[0]
	if tr["content"] != "(empty result)" {
		t.Fatalf("expected '(empty result)', got %v", tr["content"])
	}
}

func TestConvertOpenAIMessages_MultipleToolMessages(t *testing.T) {
	msgs := []models.ChatMessage{
		{Role: "tool", Content: "result1", ToolCallID: "call_a"},
		{Role: "tool", Content: "result2", ToolCallID: "call_b"},
		{Role: "user", Content: "next"},
	}
	_, unified := convertOpenAIMessages(msgs)

	// Two tool messages should be accumulated into one user message.
	if len(unified) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(unified))
	}
	if len(unified[0].ToolResults) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(unified[0].ToolResults))
	}
}

// ---------------------------------------------------------------------------
// Assistant tool_calls extraction
// ---------------------------------------------------------------------------

func TestExtractToolCallsFromOpenAI(t *testing.T) {
	raw := []any{
		map[string]any{
			"id":       "call_abc",
			"type":     "function",
			"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
		},
		map[string]any{
			"id":       "call_def",
			"type":     "function",
			"function": map[string]any{"name": "read_file", "arguments": `{"path":"/tmp"}`},
		},
	}
	result := extractToolCallsFromOpenAI(raw)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
	if result[0]["id"] != "call_abc" {
		t.Fatal("wrong id")
	}
	fn := result[0]["function"].(map[string]any)
	if fn["name"] != "bash" {
		t.Fatal("wrong function name")
	}
	if fn["arguments"] != `{"cmd":"ls"}` {
		t.Fatal("wrong arguments")
	}
}

func TestExtractToolCallsFromOpenAI_Empty(t *testing.T) {
	result := extractToolCallsFromOpenAI(nil)
	if result != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestExtractToolCallsFromOpenAI_SkipsNonMap(t *testing.T) {
	raw := []any{"not a map", 42}
	result := extractToolCallsFromOpenAI(raw)
	if len(result) != 0 {
		t.Fatal("expected empty result for non-map items")
	}
}

// ---------------------------------------------------------------------------
// Image extraction
// ---------------------------------------------------------------------------

func TestExtractImagesFromContent_DataURL(t *testing.T) {
	content := []any{
		map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "data:image/png;base64,abc123def",
			},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("expected image/png, got %s", images[0].MediaType)
	}
	if images[0].Data != "abc123def" {
		t.Fatalf("expected 'abc123def', got %s", images[0].Data)
	}
}

func TestExtractImagesFromContent_URLBasedSkipped(t *testing.T) {
	content := []any{
		map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "https://example.com/image.png",
			},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 0 {
		t.Fatal("expected URL-based images to be skipped")
	}
}

func TestExtractImagesFromContent_AnthropicBase64(t *testing.T) {
	content := []any{
		map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/jpeg",
				"data":       "jpegdata",
			},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].MediaType != "image/jpeg" {
		t.Fatal("wrong media type")
	}
	if images[0].Data != "jpegdata" {
		t.Fatal("wrong data")
	}
}

func TestExtractImagesFromContent_AnthropicURLSkipped(t *testing.T) {
	content := []any{
		map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "url",
				"url":  "https://example.com/img.jpg",
			},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 0 {
		t.Fatal("expected URL-based Anthropic images to be skipped")
	}
}

func TestExtractImagesFromContent_NotAList(t *testing.T) {
	images := extractImagesFromContent("just a string")
	if images != nil {
		t.Fatal("expected nil for non-list content")
	}
}

func TestExtractImagesFromContent_MixedContent(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "Look at this:"},
		map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:image/webp;base64,webpdata"},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 1 {
		t.Fatalf("expected 1 image from mixed content, got %d", len(images))
	}
	if images[0].MediaType != "image/webp" {
		t.Fatal("wrong media type")
	}
}

func TestExtractImagesFromContent_EmptyDataURL(t *testing.T) {
	content := []any{
		map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:image/png;base64,"},
		},
	}
	images := extractImagesFromContent(content)
	if len(images) != 0 {
		t.Fatal("expected empty data URL to be skipped")
	}
}

func TestExtractImagesFromContent_MissingImageURLField(t *testing.T) {
	content := []any{
		map[string]any{"type": "image_url"},
	}
	images := extractImagesFromContent(content)
	if len(images) != 0 {
		t.Fatal("expected nil image_url to be skipped")
	}
}

// ---------------------------------------------------------------------------
// Tool conversion
// ---------------------------------------------------------------------------

func TestConvertOpenAITools_StandardFormat(t *testing.T) {
	tools := []models.Tool{
		{
			Type: "function",
			Function: &models.ToolFunction{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
			},
		},
	}
	result := convertOpenAITools(tools)
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

func TestConvertOpenAITools_CursorFlatFormat(t *testing.T) {
	tools := []models.Tool{
		{
			Type:        "function",
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		},
	}
	result := convertOpenAITools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Name != "read_file" {
		t.Fatal("wrong name for flat format")
	}
	if result[0].Description != "Read a file" {
		t.Fatal("wrong description for flat format")
	}
}

func TestConvertOpenAITools_StandardTakesPriority(t *testing.T) {
	tools := []models.Tool{
		{
			Type: "function",
			Function: &models.ToolFunction{
				Name:        "standard_name",
				Description: "Standard desc",
				Parameters:  map[string]any{},
			},
			Name:        "flat_name",
			Description: "Flat desc",
		},
	}
	result := convertOpenAITools(tools)
	if len(result) != 1 {
		t.Fatal("expected 1 tool")
	}
	if result[0].Name != "standard_name" {
		t.Fatal("standard format should take priority over flat format")
	}
}

func TestConvertOpenAITools_SkipsNonFunction(t *testing.T) {
	tools := []models.Tool{
		{Type: "retrieval"},
		{Type: "function", Function: &models.ToolFunction{Name: "valid", Parameters: map[string]any{}}},
	}
	result := convertOpenAITools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool (non-function skipped), got %d", len(result))
	}
}

func TestConvertOpenAITools_Empty(t *testing.T) {
	result := convertOpenAITools(nil)
	if result != nil {
		t.Fatal("expected nil for empty tools")
	}
}

func TestConvertOpenAITools_InvalidToolSkipped(t *testing.T) {
	tools := []models.Tool{
		{Type: "function"}, // no function, no name
	}
	result := convertOpenAITools(tools)
	if result != nil {
		t.Fatal("expected nil when all tools are invalid")
	}
}

func TestConvertOpenAITools_NilParameters(t *testing.T) {
	tools := []models.Tool{
		{
			Type:     "function",
			Function: &models.ToolFunction{Name: "no_params"},
		},
	}
	result := convertOpenAITools(tools)
	if len(result) != 1 {
		t.Fatal("expected 1 tool")
	}
	if result[0].InputSchema == nil {
		t.Fatal("expected empty map for nil parameters, not nil")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest integration
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest_Basic(t *testing.T) {
	cfg := openaiTestCfg()
	req := models.ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []models.ChatMessage{
			{Role: "system", Content: "Be helpful"},
			{Role: "user", Content: "Hello"},
		},
	}

	result, err := ConvertOpenAIRequest(req, cfg)
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

func TestConvertOpenAIRequest_WithTools(t *testing.T) {
	cfg := openaiTestCfg()
	req := models.ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []models.ChatMessage{
			{Role: "user", Content: "Check weather"},
		},
		Tools: []models.Tool{
			{
				Type: "function",
				Function: &models.ToolFunction{
					Name:        "get_weather",
					Description: "Get weather",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
	}

	result, err := ConvertOpenAIRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
}

// ---------------------------------------------------------------------------
// BuildOpenAIKiroPayload integration
// ---------------------------------------------------------------------------

func TestBuildOpenAIKiroPayload_Basic(t *testing.T) {
	cfg := openaiTestCfg()
	req := models.ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []models.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "How are you?"},
		},
	}

	result, err := BuildOpenAIKiroPayload(req, "conv-123", "arn:aws:test", "claude-sonnet-4", cfg)
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

func TestBuildOpenAIKiroPayload_WithToolConversation(t *testing.T) {
	cfg := openaiTestCfg()
	req := models.ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []models.ChatMessage{
			{Role: "user", Content: "List files"},
			{Role: "assistant", Content: "I'll check", ToolCalls: []any{
				map[string]any{
					"id":       "call_1",
					"type":     "function",
					"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
				},
			}},
			{Role: "tool", Content: "file1.txt", ToolCallID: "call_1"},
			{Role: "user", Content: "Thanks"},
		},
		Tools: []models.Tool{
			{
				Type:     "function",
				Function: &models.ToolFunction{Name: "bash", Description: "Run bash", Parameters: map[string]any{"type": "object"}},
			},
		},
	}

	result, err := BuildOpenAIKiroPayload(req, "conv-456", "", "claude-sonnet-4", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)

	// The pipeline merges consecutive same-role messages, so the exact
	// history count depends on merging. Just verify the payload built
	// successfully and the current message is the last user message.
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
	_ = history
}

func TestBuildOpenAIKiroPayload_ToolNameValidation(t *testing.T) {
	cfg := openaiTestCfg()
	longName := strings.Repeat("a", 70)
	req := models.ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []models.ChatMessage{
			{Role: "user", Content: "test"},
		},
		Tools: []models.Tool{
			{
				Type:     "function",
				Function: &models.ToolFunction{Name: longName, Description: "test", Parameters: map[string]any{}},
			},
		},
	}

	_, err := BuildOpenAIKiroPayload(req, "conv-1", "", "claude-sonnet-4", cfg)
	if err == nil {
		t.Fatal("expected error for tool name exceeding 64 characters")
	}
	if !strings.Contains(err.Error(), "64 characters") {
		t.Fatalf("error should mention 64 characters: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Content block handling
// ---------------------------------------------------------------------------

func TestConvertOpenAIMessages_ContentBlocks(t *testing.T) {
	msgs := []models.ChatMessage{
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "Look at this image:"},
				map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": "data:image/jpeg;base64,/9j/abc"},
				},
			},
		},
	}
	_, unified := convertOpenAIMessages(msgs)

	if len(unified) != 1 {
		t.Fatalf("expected 1 message, got %d", len(unified))
	}
	if unified[0].Content != "Look at this image:" {
		t.Fatalf("expected text content extracted, got %q", unified[0].Content)
	}
	if len(unified[0].Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(unified[0].Images))
	}
	if unified[0].Images[0].MediaType != "image/jpeg" {
		t.Fatal("wrong media type")
	}
	if unified[0].Images[0].Data != "/9j/abc" {
		t.Fatal("wrong image data")
	}
}

func TestConvertOpenAIMessages_ToolMessageWithImages(t *testing.T) {
	msgs := []models.ChatMessage{
		{
			Role: "tool",
			Content: []any{
				map[string]any{"type": "text", "text": "Screenshot captured"},
				map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": "data:image/png;base64,screenshotdata"},
				},
			},
			ToolCallID: "call_mcp",
		},
		{Role: "user", Content: "What do you see?"},
	}
	_, unified := convertOpenAIMessages(msgs)

	if len(unified) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(unified))
	}

	// First message: user with tool results and images from tool message.
	toolMsg := unified[0]
	if toolMsg.Role != "user" {
		t.Fatal("expected user role for tool result message")
	}
	if len(toolMsg.ToolResults) != 1 {
		t.Fatal("expected 1 tool result")
	}
	if len(toolMsg.Images) != 1 {
		t.Fatalf("expected 1 image from tool message, got %d", len(toolMsg.Images))
	}
	if toolMsg.Images[0].Data != "screenshotdata" {
		t.Fatal("wrong image data from tool message")
	}
}

// ---------------------------------------------------------------------------
// extractToolResultsFromOpenAIContent
// ---------------------------------------------------------------------------

func TestExtractToolResultsFromOpenAIContent_WithToolResults(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "tu_123",
			"content":     "result text",
		},
		map[string]any{
			"type": "text",
			"text": "some text",
		},
	}
	results := extractToolResultsFromOpenAIContent(content)
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(results))
	}
	if results[0]["tool_use_id"] != "tu_123" {
		t.Fatal("wrong tool_use_id")
	}
	if results[0]["content"] != "result text" {
		t.Fatal("wrong content")
	}
}

func TestExtractToolResultsFromOpenAIContent_StringContent(t *testing.T) {
	results := extractToolResultsFromOpenAIContent("just a string")
	if results != nil {
		t.Fatal("expected nil for string content")
	}
}

func TestExtractToolResultsFromOpenAIContent_EmptyToolResult(t *testing.T) {
	content := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": "tu_456",
			"content":     "",
		},
	}
	results := extractToolResultsFromOpenAIContent(content)
	if len(results) != 1 {
		t.Fatal("expected 1 result")
	}
	if results[0]["content"] != "(empty result)" {
		t.Fatalf("expected '(empty result)', got %v", results[0]["content"])
	}
}

// ---------------------------------------------------------------------------
// Data URL stripping edge cases
// ---------------------------------------------------------------------------

func TestExtractOpenAIImageURL_DefaultMediaType(t *testing.T) {
	block := map[string]any{
		"image_url": map[string]any{
			"url": "data:;base64,somedata",
		},
	}
	img := extractOpenAIImageURL(block)
	if img == nil {
		t.Fatal("expected image")
	}
	// When media type is empty after stripping "data:", default to image/jpeg.
	if img.MediaType != "image/jpeg" {
		t.Fatalf("expected default image/jpeg, got %s", img.MediaType)
	}
}

func TestExtractOpenAIImageURL_EmptyURL(t *testing.T) {
	block := map[string]any{
		"image_url": map[string]any{"url": ""},
	}
	img := extractOpenAIImageURL(block)
	if img != nil {
		t.Fatal("expected nil for empty URL")
	}
}

func TestExtractAnthropicImage_EmptyData(t *testing.T) {
	block := map[string]any{
		"source": map[string]any{
			"type":       "base64",
			"media_type": "image/png",
			"data":       "",
		},
	}
	img := extractAnthropicImage(block)
	if img != nil {
		t.Fatal("expected nil for empty data")
	}
}

func TestExtractAnthropicImage_DefaultMediaType(t *testing.T) {
	block := map[string]any{
		"source": map[string]any{
			"type": "base64",
			"data": "somedata",
		},
	}
	img := extractAnthropicImage(block)
	if img == nil {
		t.Fatal("expected image")
	}
	if img.MediaType != "image/jpeg" {
		t.Fatalf("expected default image/jpeg, got %s", img.MediaType)
	}
}
