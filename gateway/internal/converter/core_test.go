package converter

import (
	"strings"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// testCfg returns a Config with sensible defaults for testing.
func testCfg() *config.Config {
	return &config.Config{
		FakeReasoningEnabled:     true,
		FakeReasoningMaxTokens:   4000,
		TruncationRecovery:       true,
		ToolDescriptionMaxLength: 10000,
	}
}

// ---------------------------------------------------------------------------
// SanitizeJSONSchema
// ---------------------------------------------------------------------------

func TestSanitizeJSONSchema_RemovesEmptyRequired(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{},
	}
	result := SanitizeJSONSchema(schema)
	if _, ok := result["required"]; ok {
		t.Fatal("expected empty required to be removed")
	}
	if result["type"] != "object" {
		t.Fatal("expected type to be preserved")
	}
}

func TestSanitizeJSONSchema_KeepsNonEmptyRequired(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"name"},
	}
	result := SanitizeJSONSchema(schema)
	req, ok := result["required"].([]any)
	if !ok || len(req) != 1 {
		t.Fatal("expected non-empty required to be preserved")
	}
}

func TestSanitizeJSONSchema_RemovesAdditionalProperties(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
	result := SanitizeJSONSchema(schema)
	if _, ok := result["additionalProperties"]; ok {
		t.Fatal("expected additionalProperties to be removed")
	}
}

func TestSanitizeJSONSchema_RecursesIntoProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"required":             []any{},
			},
		},
	}
	result := SanitizeJSONSchema(schema)
	props := result["properties"].(map[string]any)
	nested := props["nested"].(map[string]any)
	if _, ok := nested["additionalProperties"]; ok {
		t.Fatal("expected nested additionalProperties to be removed")
	}
	if _, ok := nested["required"]; ok {
		t.Fatal("expected nested empty required to be removed")
	}
}

func TestSanitizeJSONSchema_RecursesIntoAnyOf(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{
				"type":                 "string",
				"additionalProperties": false,
			},
			map[string]any{
				"type": "number",
			},
		},
	}
	result := SanitizeJSONSchema(schema)
	anyOf := result["anyOf"].([]any)
	first := anyOf[0].(map[string]any)
	if _, ok := first["additionalProperties"]; ok {
		t.Fatal("expected additionalProperties removed from anyOf item")
	}
}

func TestSanitizeJSONSchema_NilInput(t *testing.T) {
	result := SanitizeJSONSchema(nil)
	if len(result) != 0 {
		t.Fatal("expected empty map for nil input")
	}
}

// ---------------------------------------------------------------------------
// Tool processing
// ---------------------------------------------------------------------------

func TestValidateToolNames_Valid(t *testing.T) {
	tools := []UnifiedTool{{Name: "short_name", Description: "test"}}
	if err := validateToolNames(tools); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolNames_TooLong(t *testing.T) {
	longName := strings.Repeat("a", 70)
	tools := []UnifiedTool{{Name: longName, Description: "test"}}
	err := validateToolNames(tools)
	if err == nil {
		t.Fatal("expected error for long tool name")
	}
	if !strings.Contains(err.Error(), "64 characters") {
		t.Fatalf("error should mention 64 characters: %v", err)
	}
}

func TestValidateToolNames_Empty(t *testing.T) {
	if err := validateToolNames(nil); err != nil {
		t.Fatalf("unexpected error for nil tools: %v", err)
	}
}

func TestProcessToolsWithLongDescriptions_Short(t *testing.T) {
	tools := []UnifiedTool{{Name: "t1", Description: "short"}}
	processed, doc := processToolsWithLongDescriptions(tools, 10000)
	if len(processed) != 1 || processed[0].Description != "short" {
		t.Fatal("short description should be unchanged")
	}
	if doc != "" {
		t.Fatal("no documentation expected for short descriptions")
	}
}

func TestProcessToolsWithLongDescriptions_Long(t *testing.T) {
	longDesc := strings.Repeat("x", 10001)
	tools := []UnifiedTool{{Name: "big_tool", Description: longDesc}}
	processed, doc := processToolsWithLongDescriptions(tools, 10000)
	if len(processed) != 1 {
		t.Fatal("expected one processed tool")
	}
	if !strings.Contains(processed[0].Description, "system prompt") {
		t.Fatal("expected reference description")
	}
	if !strings.Contains(doc, "big_tool") {
		t.Fatal("expected tool name in documentation")
	}
	if !strings.Contains(doc, longDesc) {
		t.Fatal("expected full description in documentation")
	}
}

func TestProcessToolsWithLongDescriptions_DisabledLimit(t *testing.T) {
	longDesc := strings.Repeat("x", 20000)
	tools := []UnifiedTool{{Name: "t1", Description: longDesc}}
	processed, doc := processToolsWithLongDescriptions(tools, 0)
	if len(processed) != 1 || processed[0].Description != longDesc {
		t.Fatal("disabled limit should leave description unchanged")
	}
	if doc != "" {
		t.Fatal("no documentation expected when limit disabled")
	}
}

func TestConvertToolsToKiroFormat(t *testing.T) {
	tools := []UnifiedTool{
		{
			Name:        "get_weather",
			Description: "Get weather",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		},
	}
	result := convertToolsToKiroFormat(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	spec := result[0]["toolSpecification"].(map[string]any)
	if spec["name"] != "get_weather" {
		t.Fatal("wrong tool name")
	}
	// additionalProperties should be sanitised away.
	schema := spec["inputSchema"].(map[string]any)["json"].(map[string]any)
	if _, ok := schema["additionalProperties"]; ok {
		t.Fatal("additionalProperties should be removed")
	}
}

func TestConvertToolsToKiroFormat_EmptyDescription(t *testing.T) {
	tools := []UnifiedTool{{Name: "my_tool", Description: "", InputSchema: map[string]any{}}}
	result := convertToolsToKiroFormat(tools)
	spec := result[0]["toolSpecification"].(map[string]any)
	if spec["description"] != "Tool: my_tool" {
		t.Fatalf("expected placeholder description, got %v", spec["description"])
	}
}

// ---------------------------------------------------------------------------
// Image conversion
// ---------------------------------------------------------------------------

func TestConvertImagesToKiroFormat_Basic(t *testing.T) {
	images := []UnifiedImage{
		{MediaType: "image/png", Data: "abc123"},
	}
	result := convertImagesToKiroFormat(images)
	if len(result) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result))
	}
	if result[0]["format"] != "png" {
		t.Fatalf("expected format 'png', got %v", result[0]["format"])
	}
	src := result[0]["source"].(map[string]any)
	if src["bytes"] != "abc123" {
		t.Fatal("wrong image data")
	}
}

func TestConvertImagesToKiroFormat_StripsDataURL(t *testing.T) {
	images := []UnifiedImage{
		{MediaType: "image/jpeg", Data: "data:image/webp;base64,xyz789"},
	}
	result := convertImagesToKiroFormat(images)
	if len(result) != 1 {
		t.Fatal("expected 1 image")
	}
	if result[0]["format"] != "webp" {
		t.Fatalf("expected format 'webp' from data URL, got %v", result[0]["format"])
	}
	src := result[0]["source"].(map[string]any)
	if src["bytes"] != "xyz789" {
		t.Fatalf("expected stripped data, got %v", src["bytes"])
	}
}

func TestConvertImagesToKiroFormat_SkipsEmpty(t *testing.T) {
	images := []UnifiedImage{{MediaType: "image/png", Data: ""}}
	result := convertImagesToKiroFormat(images)
	if len(result) != 0 {
		t.Fatal("expected empty result for empty data")
	}
}

func TestConvertImagesToKiroFormat_Nil(t *testing.T) {
	result := convertImagesToKiroFormat(nil)
	if result != nil {
		t.Fatal("expected nil for nil input")
	}
}

// ---------------------------------------------------------------------------
// Thinking tags
// ---------------------------------------------------------------------------

func TestInjectThinkingTags_Enabled(t *testing.T) {
	cfg := testCfg()
	result := injectThinkingTags("Hello", cfg)
	if !strings.Contains(result, "<thinking_mode>enabled</thinking_mode>") {
		t.Fatal("expected thinking_mode tag")
	}
	if !strings.Contains(result, "<max_thinking_length>4000</max_thinking_length>") {
		t.Fatal("expected max_thinking_length tag")
	}
	if !strings.HasSuffix(result, "Hello") {
		t.Fatal("expected original content at end")
	}
}

func TestInjectThinkingTags_Disabled(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false
	result := injectThinkingTags("Hello", cfg)
	if result != "Hello" {
		t.Fatal("expected unchanged content when disabled")
	}
}

func TestGetThinkingSystemPromptAddition_Enabled(t *testing.T) {
	cfg := testCfg()
	result := getThinkingSystemPromptAddition(cfg)
	if !strings.Contains(result, "Extended Thinking Mode") {
		t.Fatal("expected thinking mode text")
	}
}

func TestGetThinkingSystemPromptAddition_Disabled(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false
	result := getThinkingSystemPromptAddition(cfg)
	if result != "" {
		t.Fatal("expected empty string when disabled")
	}
}

func TestGetTruncationRecoverySystemAddition_Enabled(t *testing.T) {
	cfg := testCfg()
	result := getTruncationRecoverySystemAddition(cfg)
	if !strings.Contains(result, "Output Truncation Handling") {
		t.Fatal("expected truncation text")
	}
}

func TestGetTruncationRecoverySystemAddition_Disabled(t *testing.T) {
	cfg := testCfg()
	cfg.TruncationRecovery = false
	result := getTruncationRecoverySystemAddition(cfg)
	if result != "" {
		t.Fatal("expected empty string when disabled")
	}
}

// ---------------------------------------------------------------------------
// Tool content stripping
// ---------------------------------------------------------------------------

func TestStripAllToolContent_NoTools(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "hello"},
	}
	result, had := stripAllToolContent(msgs)
	if had {
		t.Fatal("expected no tool content")
	}
	if len(result) != 1 || result[0].Content != "hello" {
		t.Fatal("message should be unchanged")
	}
}

func TestStripAllToolContent_WithToolCalls(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:    "assistant",
			Content: "Let me check",
			ToolCalls: []map[string]any{
				{
					"id":       "call_1",
					"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
				},
			},
		},
	}
	result, had := stripAllToolContent(msgs)
	if !had {
		t.Fatal("expected tool content detected")
	}
	if len(result) != 1 {
		t.Fatal("expected 1 message")
	}
	if len(result[0].ToolCalls) != 0 {
		t.Fatal("tool calls should be stripped")
	}
	if !strings.Contains(result[0].Content, "bash") {
		t.Fatal("expected tool name in text representation")
	}
	if !strings.Contains(result[0].Content, "Let me check") {
		t.Fatal("expected original content preserved")
	}
}

func TestStripAllToolContent_WithToolResults(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:    "user",
			Content: "",
			ToolResults: []map[string]any{
				{"tool_use_id": "call_1", "content": "file1.txt"},
			},
		},
	}
	result, had := stripAllToolContent(msgs)
	if !had {
		t.Fatal("expected tool content detected")
	}
	if !strings.Contains(result[0].Content, "file1.txt") {
		t.Fatal("expected tool result in text")
	}
	if len(result[0].ToolResults) != 0 {
		t.Fatal("tool results should be stripped")
	}
}

func TestStripAllToolContent_PreservesImages(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:        "user",
			Content:     "see image",
			ToolResults: []map[string]any{{"tool_use_id": "c1", "content": "ok"}},
			Images:      []UnifiedImage{{MediaType: "image/png", Data: "abc"}},
		},
	}
	result, _ := stripAllToolContent(msgs)
	if len(result[0].Images) != 1 {
		t.Fatal("images should be preserved")
	}
}

// ---------------------------------------------------------------------------
// Message merging
// ---------------------------------------------------------------------------

func TestMergeAdjacentMessages_DifferentRoles(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	result := mergeAdjacentMessages(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
}

func TestMergeAdjacentMessages_SameRole(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a"},
		{Role: "user", Content: "b"},
		{Role: "assistant", Content: "c"},
	}
	result := mergeAdjacentMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Content != "a\nb" {
		t.Fatalf("expected merged content 'a\\nb', got '%s'", result[0].Content)
	}
}

func TestMergeAdjacentMessages_MergesToolCalls(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "assistant", Content: "a", ToolCalls: []map[string]any{{"id": "1"}}},
		{Role: "assistant", Content: "b", ToolCalls: []map[string]any{{"id": "2"}}},
	}
	result := mergeAdjacentMessages(msgs)
	if len(result) != 1 {
		t.Fatal("expected 1 merged message")
	}
	if len(result[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result[0].ToolCalls))
	}
}

func TestMergeAdjacentMessages_MergesToolResults(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a", ToolResults: []map[string]any{{"tool_use_id": "1"}}},
		{Role: "user", Content: "b", ToolResults: []map[string]any{{"tool_use_id": "2"}}},
	}
	result := mergeAdjacentMessages(msgs)
	if len(result) != 1 {
		t.Fatal("expected 1 merged message")
	}
	if len(result[0].ToolResults) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(result[0].ToolResults))
	}
}

func TestMergeAdjacentMessages_Empty(t *testing.T) {
	result := mergeAdjacentMessages(nil)
	if result != nil {
		t.Fatal("expected nil for nil input")
	}
}

// ---------------------------------------------------------------------------
// Role normalisation and alternation
// ---------------------------------------------------------------------------

func TestEnsureFirstMessageIsUser_AlreadyUser(t *testing.T) {
	msgs := []UnifiedMessage{{Role: "user", Content: "hi"}}
	result := ensureFirstMessageIsUser(msgs)
	if len(result) != 1 {
		t.Fatal("should not add message when first is user")
	}
}

func TestEnsureFirstMessageIsUser_AssistantFirst(t *testing.T) {
	msgs := []UnifiedMessage{{Role: "assistant", Content: "hi"}}
	result := ensureFirstMessageIsUser(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "(empty)" {
		t.Fatal("expected synthetic user message")
	}
}

func TestNormalizeMessageRoles_UnknownRole(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "developer", Content: "context"},
		{Role: "user", Content: "question"},
	}
	result := normalizeMessageRoles(msgs)
	if result[0].Role != "user" {
		t.Fatalf("expected 'user', got '%s'", result[0].Role)
	}
	if result[1].Role != "user" {
		t.Fatal("user role should be unchanged")
	}
}

func TestNormalizeMessageRoles_PreservesKnownRoles(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	result := normalizeMessageRoles(msgs)
	if result[0].Role != "user" || result[1].Role != "assistant" {
		t.Fatal("known roles should be preserved")
	}
}

func TestEnsureAlternatingRoles_AlreadyAlternating(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	result := ensureAlternatingRoles(msgs)
	if len(result) != 3 {
		t.Fatal("should not insert messages when already alternating")
	}
}

func TestEnsureAlternatingRoles_ConsecutiveUsers(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "a"},
		{Role: "user", Content: "b"},
		{Role: "user", Content: "c"},
	}
	result := ensureAlternatingRoles(msgs)
	// 3 user + 2 synthetic assistant = 5
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(result))
	}
	if result[1].Role != "assistant" || result[1].Content != "(empty)" {
		t.Fatal("expected synthetic assistant at index 1")
	}
	if result[3].Role != "assistant" || result[3].Content != "(empty)" {
		t.Fatal("expected synthetic assistant at index 3")
	}
}

func TestEnsureAssistantBeforeToolResults_WithPreceding(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "assistant", Content: "ok", ToolCalls: []map[string]any{{"id": "1"}}},
		{Role: "user", Content: "", ToolResults: []map[string]any{{"tool_use_id": "1", "content": "done"}}},
	}
	result, converted := ensureAssistantBeforeToolResults(msgs)
	if converted {
		t.Fatal("should not convert when preceding assistant exists")
	}
	if len(result) != 2 {
		t.Fatal("expected 2 messages")
	}
	if len(result[1].ToolResults) != 1 {
		t.Fatal("tool results should be preserved")
	}
}

func TestEnsureAssistantBeforeToolResults_Orphaned(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "hi", ToolResults: []map[string]any{{"tool_use_id": "1", "content": "result"}}},
	}
	result, converted := ensureAssistantBeforeToolResults(msgs)
	if !converted {
		t.Fatal("expected conversion of orphaned tool results")
	}
	if len(result[0].ToolResults) != 0 {
		t.Fatal("tool results should be removed")
	}
	if !strings.Contains(result[0].Content, "result") {
		t.Fatal("expected tool result text in content")
	}
}

// ---------------------------------------------------------------------------
// Tool results / tool uses conversion
// ---------------------------------------------------------------------------

func TestConvertToolResultsToKiroFormat(t *testing.T) {
	results := []map[string]any{
		{"tool_use_id": "call_1", "content": "file list"},
	}
	kiro := convertToolResultsToKiroFormat(results, 0)
	if len(kiro) != 1 {
		t.Fatalf("expected 1 result, got %d", len(kiro))
	}
	if kiro[0]["toolUseId"] != "call_1" {
		t.Fatal("wrong toolUseId")
	}
	if kiro[0]["status"] != "success" {
		t.Fatal("expected status success")
	}
	content := kiro[0]["content"].([]map[string]any)
	if content[0]["text"] != "file list" {
		t.Fatal("wrong content text")
	}
}

func TestConvertToolResultsToKiroFormat_EmptyContent(t *testing.T) {
	results := []map[string]any{
		{"tool_use_id": "call_1", "content": ""},
	}
	kiro := convertToolResultsToKiroFormat(results, 0)
	content := kiro[0]["content"].([]map[string]any)
	if content[0]["text"] != "(empty result)" {
		t.Fatal("expected placeholder for empty content")
	}
}

func TestConvertToolResultsToKiroFormat_Truncation(t *testing.T) {
	longContent := strings.Repeat("x", 25000)
	results := []map[string]any{
		{"tool_use_id": "call_1", "content": longContent},
	}
	kiro := convertToolResultsToKiroFormat(results, 20000)
	content := kiro[0]["content"].([]map[string]any)
	text := content[0]["text"].(string)
	if len(text) <= 20000 {
		t.Fatal("expected truncated content to be longer than limit due to notice")
	}
	if len(text) >= len(longContent) {
		t.Fatal("expected content to be shorter than original")
	}
	if !strings.Contains(text, "[API Limitation]") {
		t.Fatal("expected truncation notice in content")
	}
}

func TestExtractToolUsesFromMessage_OpenAIFormat(t *testing.T) {
	toolCalls := []map[string]any{
		{
			"id":       "call_abc",
			"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
		},
	}
	uses := extractToolUsesFromMessage(toolCalls)
	if len(uses) != 1 {
		t.Fatalf("expected 1 tool use, got %d", len(uses))
	}
	if uses[0]["name"] != "bash" {
		t.Fatal("wrong tool name")
	}
	if uses[0]["toolUseId"] != "call_abc" {
		t.Fatal("wrong toolUseId")
	}
}

func TestExtractToolUsesFromMessage_AnthropicFormat(t *testing.T) {
	toolCalls := []map[string]any{
		{
			"id":    "tu_123",
			"name":  "read_file",
			"input": map[string]any{"path": "/tmp/test"},
		},
	}
	uses := extractToolUsesFromMessage(toolCalls)
	if len(uses) != 1 {
		t.Fatal("expected 1 tool use")
	}
	if uses[0]["name"] != "read_file" {
		t.Fatal("wrong tool name")
	}
	input := uses[0]["input"].(map[string]any)
	if input["path"] != "/tmp/test" {
		t.Fatal("wrong input")
	}
}

// ---------------------------------------------------------------------------
// extractTextFromAny
// ---------------------------------------------------------------------------

func TestExtractTextFromAny_String(t *testing.T) {
	if extractTextFromAny("hello") != "hello" {
		t.Fatal("expected 'hello'")
	}
}

func TestExtractTextFromAny_Nil(t *testing.T) {
	if extractTextFromAny(nil) != "" {
		t.Fatal("expected empty string for nil")
	}
}

func TestExtractTextFromAny_ContentBlocks(t *testing.T) {
	blocks := []any{
		map[string]any{"type": "text", "text": "part1"},
		map[string]any{"type": "text", "text": "part2"},
	}
	result := extractTextFromAny(blocks)
	if result != "part1part2" {
		t.Fatalf("expected 'part1part2', got '%s'", result)
	}
}

// ---------------------------------------------------------------------------
// toolCallsToText / toolResultsToText
// ---------------------------------------------------------------------------

func TestToolCallsToText(t *testing.T) {
	calls := []map[string]any{
		{
			"id":       "call_1",
			"function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`},
		},
	}
	text := toolCallsToText(calls)
	if !strings.Contains(text, "[Tool: bash (call_1)]") {
		t.Fatalf("expected tool header, got: %s", text)
	}
	if !strings.Contains(text, `{"cmd":"ls"}`) {
		t.Fatal("expected arguments in text")
	}
}

func TestToolResultsToText(t *testing.T) {
	results := []map[string]any{
		{"tool_use_id": "call_1", "content": "file1.txt\nfile2.txt"},
	}
	text := toolResultsToText(results)
	if !strings.Contains(text, "[Tool Result (call_1)]") {
		t.Fatalf("expected result header, got: %s", text)
	}
	if !strings.Contains(text, "file1.txt") {
		t.Fatal("expected content in text")
	}
}

// ---------------------------------------------------------------------------
// BuildKiroPayload integration tests
// ---------------------------------------------------------------------------

func TestBuildKiroPayload_BasicConversation(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false // simplify for this test

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "How are you?"},
		},
		SystemPrompt:   "You are helpful.",
		ModelID:        "claude-sonnet-4",
		ConversationID: "conv-123",
		ProfileARN:     "arn:aws:test",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload := result.Payload
	convState := payload["conversationState"].(map[string]any)

	// Check profileArn is included.
	if payload["profileArn"] != "arn:aws:test" {
		t.Fatal("expected profileArn")
	}

	// Check conversationId.
	if convState["conversationId"] != "conv-123" {
		t.Fatal("wrong conversationId")
	}

	// Check history has 2 entries (first user + assistant).
	history := convState["history"].([]map[string]any)
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// First history entry should have system prompt prepended.
	firstUser := history[0]["userInputMessage"].(map[string]any)
	content := firstUser["content"].(string)
	if !strings.HasPrefix(content, "You are helpful.") {
		t.Fatal("expected system prompt prepended to first user message")
	}

	// Current message should be the last user message.
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	if userInput["content"] != "How are you?" {
		t.Fatalf("expected 'How are you?', got '%v'", userInput["content"])
	}
}

func TestBuildKiroPayload_NoProfileARN(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       []UnifiedMessage{{Role: "user", Content: "hi"}},
		ConversationID: "c1",
		ProfileARN:     "",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result.Payload["profileArn"]; ok {
		t.Fatal("profileArn should be omitted when empty")
	}
}

func TestBuildKiroPayload_WithTools(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{{Role: "user", Content: "check weather"}},
		Tools: []UnifiedTool{
			{Name: "get_weather", Description: "Get weather", InputSchema: map[string]any{"type": "object"}},
		},
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	ctx := userInput["userInputMessageContext"].(map[string]any)
	tools := ctx["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestBuildKiroPayload_StripsToolsWhenNoDefined(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{
			{
				Role:    "assistant",
				Content: "Let me check",
				ToolCalls: []map[string]any{
					{"id": "c1", "function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`}},
				},
			},
			{
				Role:        "user",
				Content:     "",
				ToolResults: []map[string]any{{"tool_use_id": "c1", "content": "file.txt"}},
			},
		},
		Tools:          nil, // no tools defined
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The current message should have tool content converted to text.
	convState := result.Payload["conversationState"].(map[string]any)
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	content := userInput["content"].(string)
	if !strings.Contains(content, "file.txt") {
		t.Fatal("expected tool result text in content")
	}
	// Should not have userInputMessageContext with toolResults.
	if ctx, ok := userInput["userInputMessageContext"]; ok {
		ctxMap := ctx.(map[string]any)
		if _, ok := ctxMap["toolResults"]; ok {
			t.Fatal("toolResults should not be present when no tools defined")
		}
	}
}

func TestBuildKiroPayload_ThinkingInjection(t *testing.T) {
	cfg := testCfg()

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       []UnifiedMessage{{Role: "user", Content: "Hello"}},
		ConversationID: "c1",
		InjectThinking: true,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	content := userInput["content"].(string)
	if !strings.Contains(content, "<thinking_mode>enabled</thinking_mode>") {
		t.Fatal("expected thinking tags in content")
	}
}

func TestBuildKiroPayload_SystemPromptNoHistory(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       []UnifiedMessage{{Role: "user", Content: "Hello"}},
		SystemPrompt:   "Be helpful.",
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	content := userInput["content"].(string)
	if !strings.HasPrefix(content, "Be helpful.") {
		t.Fatal("expected system prompt prepended to current message when no history")
	}
}

func TestBuildKiroPayload_AssistantLastMessage(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{
			{Role: "user", Content: "Start"},
			{Role: "assistant", Content: "I was saying..."},
		},
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	history := convState["history"].([]map[string]any)
	// Should have user + assistant in history.
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// Current message should be "Continue".
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	if userInput["content"] != "Continue" {
		t.Fatalf("expected 'Continue', got '%v'", userInput["content"])
	}
}

func TestBuildKiroPayload_EmptyMessages(t *testing.T) {
	cfg := testCfg()
	_, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       nil,
		ConversationID: "c1",
		Cfg:            cfg,
	})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestBuildKiroPayload_WithImages(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{
			{
				Role:    "user",
				Content: "What is this?",
				Images:  []UnifiedImage{{MediaType: "image/png", Data: "abc123"}},
			},
		},
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	convState := result.Payload["conversationState"].(map[string]any)
	currentMsg := convState["currentMessage"].(map[string]any)
	userInput := currentMsg["userInputMessage"].(map[string]any)
	images := userInput["images"].([]map[string]any)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0]["format"] != "png" {
		t.Fatal("wrong image format")
	}
}

func TestBuildKiroPayload_ToolDocumentation(t *testing.T) {
	cfg := testCfg()
	cfg.FakeReasoningEnabled = false

	longDesc := strings.Repeat("x", 10001)
	result, err := BuildKiroPayload(BuildKiroPayloadOptions{
		Messages: []UnifiedMessage{{Role: "user", Content: "hi"}},
		Tools: []UnifiedTool{
			{Name: "big_tool", Description: longDesc, InputSchema: map[string]any{"type": "object"}},
		},
		ConversationID: "c1",
		InjectThinking: false,
		Cfg:            cfg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.ToolDocumentation, "big_tool") {
		t.Fatal("expected tool documentation")
	}
}

// ---------------------------------------------------------------------------
// buildKiroHistory
// ---------------------------------------------------------------------------

func TestBuildKiroHistory_UserAndAssistant(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	history := buildKiroHistory(msgs, "model-1", 0)
	if len(history) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(history))
	}

	userMsg := history[0]["userInputMessage"].(map[string]any)
	if userMsg["content"] != "hello" {
		t.Fatal("wrong user content")
	}
	if userMsg["modelId"] != "model-1" {
		t.Fatal("wrong modelId")
	}

	assistantMsg := history[1]["assistantResponseMessage"].(map[string]any)
	if assistantMsg["content"] != "hi" {
		t.Fatal("wrong assistant content")
	}
}

func TestBuildKiroHistory_EmptyContent(t *testing.T) {
	msgs := []UnifiedMessage{
		{Role: "user", Content: ""},
	}
	history := buildKiroHistory(msgs, "m1", 0)
	userMsg := history[0]["userInputMessage"].(map[string]any)
	if userMsg["content"] != "(empty)" {
		t.Fatal("expected (empty) placeholder for empty content")
	}
}

func TestBuildKiroHistory_WithToolResults(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:        "user",
			Content:     "result",
			ToolResults: []map[string]any{{"tool_use_id": "c1", "content": "output"}},
		},
	}
	history := buildKiroHistory(msgs, "m1", 0)
	userMsg := history[0]["userInputMessage"].(map[string]any)
	ctx := userMsg["userInputMessageContext"].(map[string]any)
	results := ctx["toolResults"].([]map[string]any)
	if len(results) != 1 {
		t.Fatal("expected 1 tool result")
	}
}

func TestBuildKiroHistory_WithToolUses(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []map[string]any{
				{"id": "c1", "function": map[string]any{"name": "bash", "arguments": `{"cmd":"ls"}`}},
			},
		},
	}
	history := buildKiroHistory(msgs, "m1", 0)
	assistantMsg := history[0]["assistantResponseMessage"].(map[string]any)
	toolUses := assistantMsg["toolUses"].([]map[string]any)
	if len(toolUses) != 1 {
		t.Fatal("expected 1 tool use")
	}
	if toolUses[0]["name"] != "bash" {
		t.Fatal("wrong tool name")
	}
}

func TestBuildKiroHistory_WithImages(t *testing.T) {
	msgs := []UnifiedMessage{
		{
			Role:    "user",
			Content: "see this",
			Images:  []UnifiedImage{{MediaType: "image/jpeg", Data: "imgdata"}},
		},
	}
	history := buildKiroHistory(msgs, "m1", 0)
	userMsg := history[0]["userInputMessage"].(map[string]any)
	images := userMsg["images"].([]map[string]any)
	if len(images) != 1 {
		t.Fatal("expected 1 image in history")
	}
}
