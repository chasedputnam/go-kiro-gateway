package models

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// OpenAI Models
// ---------------------------------------------------------------------------

func TestChatCompletionRequest_MarshalUnmarshal(t *testing.T) {
	temp := 0.7
	topP := 0.9
	maxTok := 1024

	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Stream:      true,
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   &maxTok,
		Tools: []Tool{
			{
				Type: "function",
				Function: &ToolFunction{
					Name:        "get_weather",
					Description: "Get weather info",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ChatCompletionRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Model != req.Model {
		t.Errorf("Model = %q, want %q", got.Model, req.Model)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(got.Messages))
	}
	if got.Stream != true {
		t.Error("Stream should be true")
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", got.TopP)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %v, want 1024", got.MaxTokens)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("Tools len = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Function == nil || got.Tools[0].Function.Name != "get_weather" {
		t.Error("Tool function not deserialized correctly")
	}
}

func TestChatMessage_StringContent(t *testing.T) {
	msg := ChatMessage{Role: "user", Content: "Hello world"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ChatMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	str, ok := got.Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", got.Content)
	}
	if str != "Hello world" {
		t.Errorf("Content = %q, want %q", str, "Hello world")
	}
}

func TestChatMessage_ArrayContent(t *testing.T) {
	raw := `{
		"role": "user",
		"content": [
			{"type": "text", "text": "What is this?"},
			{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc123"}}
		]
	}`

	var msg ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Role != "user" {
		t.Errorf("Role = %q, want %q", msg.Role, "user")
	}

	blocks, ok := msg.Content.([]any)
	if !ok {
		t.Fatalf("Content type = %T, want []any", msg.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("Content blocks len = %d, want 2", len(blocks))
	}

	first, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatalf("first block type = %T, want map[string]any", blocks[0])
	}
	if first["type"] != "text" {
		t.Errorf("first block type = %v, want text", first["type"])
	}
}

func TestTool_StandardFormat(t *testing.T) {
	raw := `{
		"type": "function",
		"function": {
			"name": "get_weather",
			"description": "Get weather for a location",
			"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
		}
	}`

	var tool Tool
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if tool.Type != "function" {
		t.Errorf("Type = %q, want %q", tool.Type, "function")
	}
	if tool.Function == nil {
		t.Fatal("Function is nil")
	}
	if tool.Function.Name != "get_weather" {
		t.Errorf("Function.Name = %q, want %q", tool.Function.Name, "get_weather")
	}
	if tool.Function.Description != "Get weather for a location" {
		t.Errorf("Function.Description = %q", tool.Function.Description)
	}
	if tool.Function.Parameters == nil {
		t.Error("Function.Parameters is nil")
	}
}

func TestTool_CursorFlatFormat(t *testing.T) {
	raw := `{
		"type": "function",
		"name": "read_file",
		"description": "Read a file from disk",
		"input_schema": {"type": "object", "properties": {"path": {"type": "string"}}}
	}`

	var tool Tool
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if tool.Type != "function" {
		t.Errorf("Type = %q, want %q", tool.Type, "function")
	}
	if tool.Function != nil {
		t.Error("Function should be nil for Cursor flat format")
	}
	if tool.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tool.Name, "read_file")
	}
	if tool.Description != "Read a file from disk" {
		t.Errorf("Description = %q", tool.Description)
	}
	if tool.InputSchema == nil {
		t.Error("InputSchema is nil")
	}
	if tool.InputSchema["type"] != "object" {
		t.Errorf("InputSchema type = %v, want object", tool.InputSchema["type"])
	}
}

func TestChatCompletionResponse_FullRoundTrip(t *testing.T) {
	finish := "stop"
	credits := 1.5

	resp := ChatCompletionResponse{
		ID:      "chatcmpl-abc123",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "gpt-4",
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Message:      map[string]any{"role": "assistant", "content": "Hello!"},
				FinishReason: &finish,
			},
		},
		Usage: ChatCompletionUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			CreditsUsed:      &credits,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ChatCompletionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != "chatcmpl-abc123" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Object != "chat.completion" {
		t.Errorf("Object = %q", got.Object)
	}
	if got.Created != 1700000000 {
		t.Errorf("Created = %d", got.Created)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("Choices len = %d", len(got.Choices))
	}
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %v", got.Choices[0].FinishReason)
	}
	if got.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d", got.Usage.PromptTokens)
	}
	if got.Usage.CreditsUsed == nil || *got.Usage.CreditsUsed != 1.5 {
		t.Errorf("CreditsUsed = %v", got.Usage.CreditsUsed)
	}
}

func TestChatCompletionChoice_NilFinishReason_SerializesAsNull(t *testing.T) {
	choice := ChatCompletionChoice{
		Index:        0,
		Delta:        map[string]any{"content": "hi"},
		FinishReason: nil,
	}

	data, err := json.Marshal(choice)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	val, exists := raw["finish_reason"]
	if !exists {
		t.Fatal("finish_reason key should be present in JSON")
	}
	if val != nil {
		t.Errorf("finish_reason = %v, want null", val)
	}
}

func TestChatCompletionUsage_NilCreditsUsed_OmitsField(t *testing.T) {
	usage := ChatCompletionUsage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		CreditsUsed:      nil,
	}

	data, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	if _, exists := raw["credits_used"]; exists {
		t.Error("credits_used should be absent when nil (omitempty)")
	}
}

func TestOpenAI_UnknownFields_SilentlyIgnored(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hi"}],
		"unknown_field": "should be ignored",
		"another_unknown": 42,
		"nested_unknown": {"key": "value"}
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal should not fail on unknown fields: %v", err)
	}

	if req.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", req.Model, "gpt-4")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(req.Messages))
	}
}

// ---------------------------------------------------------------------------
// Anthropic Models
// ---------------------------------------------------------------------------

func TestAnthropicMessagesRequest_SystemAsString(t *testing.T) {
	raw := `{
		"model": "claude-sonnet-4-20250514",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 1024,
		"system": "You are a helpful assistant."
	}`

	var req AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d", req.MaxTokens)
	}

	sys, ok := req.System.(string)
	if !ok {
		t.Fatalf("System type = %T, want string", req.System)
	}
	if sys != "You are a helpful assistant." {
		t.Errorf("System = %q", sys)
	}
}

func TestAnthropicMessagesRequest_SystemAsContentBlocks(t *testing.T) {
	raw := `{
		"model": "claude-sonnet-4-20250514",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 1024,
		"system": [
			{"type": "text", "text": "You are helpful."},
			{"type": "text", "text": "Be concise.", "cache_control": {"type": "ephemeral"}}
		]
	}`

	var req AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	blocks, ok := req.System.([]any)
	if !ok {
		t.Fatalf("System type = %T, want []any", req.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("System blocks len = %d, want 2", len(blocks))
	}

	first, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatalf("first block type = %T, want map[string]any", blocks[0])
	}
	if first["type"] != "text" {
		t.Errorf("first block type = %v", first["type"])
	}
	if first["text"] != "You are helpful." {
		t.Errorf("first block text = %v", first["text"])
	}
}

func TestAnthropicMessage_ContentAsString(t *testing.T) {
	raw := `{"role": "user", "content": "Hello"}`

	var msg AnthropicMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	str, ok := msg.Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", msg.Content)
	}
	if str != "Hello" {
		t.Errorf("Content = %q", str)
	}
}

func TestAnthropicMessage_ContentAsArray(t *testing.T) {
	raw := `{
		"role": "user",
		"content": [
			{"type": "text", "text": "Describe this image"},
			{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "abc123"}}
		]
	}`

	var msg AnthropicMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	blocks, ok := msg.Content.([]any)
	if !ok {
		t.Fatalf("Content type = %T, want []any", msg.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("Content blocks len = %d, want 2", len(blocks))
	}
}

func TestAnthropicTool_Marshal(t *testing.T) {
	tool := AnthropicTool{
		Name:        "get_weather",
		Description: "Get weather for a city",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"required": []any{"city"},
		},
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got AnthropicTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "get_weather" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "Get weather for a city" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.InputSchema == nil {
		t.Error("InputSchema is nil")
	}
}

func TestAnthropicMessagesResponse_NilStopReason_SerializesAsNull(t *testing.T) {
	resp := AnthropicMessagesResponse{
		ID:         "msg_abc123",
		Type:       "message",
		Role:       "assistant",
		Content:    []map[string]any{{"type": "text", "text": "Hello"}},
		Model:      "claude-sonnet-4-20250514",
		StopReason: nil,
		Usage:      AnthropicUsage{InputTokens: 10, OutputTokens: 5},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	val, exists := raw["stop_reason"]
	if !exists {
		t.Fatal("stop_reason key should be present in JSON")
	}
	if val != nil {
		t.Errorf("stop_reason = %v, want null", val)
	}

	// stop_sequence should also be null
	val2, exists2 := raw["stop_sequence"]
	if !exists2 {
		t.Fatal("stop_sequence key should be present in JSON")
	}
	if val2 != nil {
		t.Errorf("stop_sequence = %v, want null", val2)
	}
}

func TestAnthropic_UnknownFields_SilentlyIgnored(t *testing.T) {
	raw := `{
		"model": "claude-sonnet-4-20250514",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 1024,
		"unknown_field": "should be ignored",
		"metadata": {"user_id": "abc"}
	}`

	var req AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal should not fail on unknown fields: %v", err)
	}

	if req.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d", req.MaxTokens)
	}
}

func TestAnthropicMessagesRequest_OptionalPointerFields(t *testing.T) {
	temp := 0.5
	topP := 0.8
	topK := 40

	req := AnthropicMessagesRequest{
		Model:       "claude-sonnet-4-20250514",
		Messages:    []AnthropicMessage{{Role: "user", Content: "hi"}},
		MaxTokens:   1024,
		Temperature: &temp,
		TopP:        &topP,
		TopK:        &topK,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got AnthropicMessagesRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Temperature == nil || *got.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.8 {
		t.Errorf("TopP = %v, want 0.8", got.TopP)
	}
	if got.TopK == nil || *got.TopK != 40 {
		t.Errorf("TopK = %v, want 40", got.TopK)
	}
}

func TestAnthropicMessagesRequest_NilOptionalFields_Omitted(t *testing.T) {
	req := AnthropicMessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		Messages:  []AnthropicMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 1024,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	for _, field := range []string{"temperature", "top_p", "top_k", "system", "tools", "tool_choice"} {
		if _, exists := raw[field]; exists {
			t.Errorf("field %q should be absent when nil/zero (omitempty)", field)
		}
	}
}

// ---------------------------------------------------------------------------
// Kiro Models
// ---------------------------------------------------------------------------

func TestKiroPayload_FullRoundTrip(t *testing.T) {
	payload := KiroPayload{
		ConversationState: ConversationState{
			ConversationID: "conv-123",
			CurrentMessage: CurrentMessage{
				UserInputMessage: UserInputMessage{
					Content: "Hello, world!",
					Images: []KiroImage{
						{
							Format: "png",
							Source: KiroImageSource{Bytes: "base64data"},
						},
					},
				},
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{
						{
							Content:   []KiroTextContent{{Text: "result text"}},
							Status:    "success",
							ToolUseID: "tool-1",
						},
					},
				},
			},
			ChatTriggerType:  "MANUAL",
			CustomizationARN: "arn:aws:codewhisperer:us-east-1:123456789:customization/test",
			History: []HistoryMessage{
				{"userInputMessage": map[string]any{"content": "previous question"}},
				{"assistantResponseMessage": map[string]any{"content": "previous answer"}},
			},
		},
		ProfileARN:      "arn:aws:codewhisperer:us-east-1:123456789:profile/test",
		Source:          "kiro-gateway",
		StreamingFormat: "event-stream",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got KiroPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ConversationState.ConversationID != "conv-123" {
		t.Errorf("ConversationID = %q", got.ConversationState.ConversationID)
	}
	if got.ConversationState.CurrentMessage.UserInputMessage.Content != "Hello, world!" {
		t.Errorf("Content = %q", got.ConversationState.CurrentMessage.UserInputMessage.Content)
	}
	if len(got.ConversationState.CurrentMessage.UserInputMessage.Images) != 1 {
		t.Fatalf("Images len = %d, want 1", len(got.ConversationState.CurrentMessage.UserInputMessage.Images))
	}
	if got.ConversationState.CurrentMessage.UserInputMessage.Images[0].Format != "png" {
		t.Errorf("Image format = %q", got.ConversationState.CurrentMessage.UserInputMessage.Images[0].Format)
	}
	if got.ConversationState.CurrentMessage.UserInputMessageContext == nil {
		t.Fatal("UserInputMessageContext is nil")
	}
	if len(got.ConversationState.CurrentMessage.UserInputMessageContext.ToolResults) != 1 {
		t.Fatalf("ToolResults len = %d", len(got.ConversationState.CurrentMessage.UserInputMessageContext.ToolResults))
	}
	tr := got.ConversationState.CurrentMessage.UserInputMessageContext.ToolResults[0]
	if tr.ToolUseID != "tool-1" {
		t.Errorf("ToolUseID = %q", tr.ToolUseID)
	}
	if tr.Status != "success" {
		t.Errorf("Status = %q", tr.Status)
	}
	if len(tr.Content) != 1 || tr.Content[0].Text != "result text" {
		t.Errorf("ToolResult content = %v", tr.Content)
	}
	if len(got.ConversationState.History) != 2 {
		t.Fatalf("History len = %d, want 2", len(got.ConversationState.History))
	}
	if got.ProfileARN != "arn:aws:codewhisperer:us-east-1:123456789:profile/test" {
		t.Errorf("ProfileARN = %q", got.ProfileARN)
	}
	if got.Source != "kiro-gateway" {
		t.Errorf("Source = %q", got.Source)
	}
	if got.StreamingFormat != "event-stream" {
		t.Errorf("StreamingFormat = %q", got.StreamingFormat)
	}
}

func TestKiroPayload_OmitEmptyFields(t *testing.T) {
	payload := KiroPayload{
		ConversationState: ConversationState{
			ConversationID: "conv-1",
			CurrentMessage: CurrentMessage{
				UserInputMessage: UserInputMessage{Content: "hi"},
			},
			ChatTriggerType: "MANUAL",
		},
		Source:          "kiro-gateway",
		StreamingFormat: "event-stream",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// profileArn should be absent when empty (omitempty)
	if _, exists := raw["profileArn"]; exists {
		t.Error("profileArn should be absent when empty")
	}

	// Check conversationState.history is absent when nil
	cs, ok := raw["conversationState"].(map[string]any)
	if !ok {
		t.Fatal("conversationState not a map")
	}
	if _, exists := cs["history"]; exists {
		t.Error("history should be absent when nil (omitempty)")
	}

	// Check currentMessage.userInputMessageContext is absent when nil
	cm, ok := cs["currentMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage not a map")
	}
	if _, exists := cm["userInputMessageContext"]; exists {
		t.Error("userInputMessageContext should be absent when nil (omitempty)")
	}
}

func TestKiroToolResult_Marshal(t *testing.T) {
	tr := KiroToolResult{
		Content:   []KiroTextContent{{Text: "file contents here"}},
		Status:    "success",
		ToolUseID: "call_abc123",
	}

	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	if raw["toolUseId"] != "call_abc123" {
		t.Errorf("toolUseId = %v", raw["toolUseId"])
	}
	if raw["status"] != "success" {
		t.Errorf("status = %v", raw["status"])
	}

	content, ok := raw["content"].([]any)
	if !ok {
		t.Fatalf("content type = %T", raw["content"])
	}
	if len(content) != 1 {
		t.Fatalf("content len = %d", len(content))
	}
}

func TestModelInfo_Marshal(t *testing.T) {
	info := ModelInfo{
		ModelID:        "claude-sonnet-4",
		MaxInputTokens: 200000,
		DisplayName:    "Claude Sonnet 4",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ModelInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ModelID != "claude-sonnet-4" {
		t.Errorf("ModelID = %q", got.ModelID)
	}
	if got.MaxInputTokens != 200000 {
		t.Errorf("MaxInputTokens = %d", got.MaxInputTokens)
	}
	if got.DisplayName != "Claude Sonnet 4" {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}
}

func TestToolSpecification_Marshal(t *testing.T) {
	spec := ToolSpecification{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ToolSpecification
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "read_file" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "Read a file from disk" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.InputSchema == nil {
		t.Error("InputSchema is nil")
	}
}

func TestHistoryMessage_UserInputMessage(t *testing.T) {
	msg := HistoryMessage{
		"userInputMessage": map[string]any{
			"content": "What is Go?",
			"images":  []any{},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got HistoryMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	uim, ok := got["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatalf("userInputMessage type = %T", got["userInputMessage"])
	}
	if uim["content"] != "What is Go?" {
		t.Errorf("content = %v", uim["content"])
	}
}

func TestHistoryMessage_AssistantResponseMessage(t *testing.T) {
	msg := HistoryMessage{
		"assistantResponseMessage": map[string]any{
			"content": "Go is a programming language.",
			"toolUses": []any{
				map[string]any{
					"toolUseId": "tool-1",
					"name":      "search",
					"input":     map[string]any{"query": "Go language"},
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got HistoryMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	arm, ok := got["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatalf("assistantResponseMessage type = %T", got["assistantResponseMessage"])
	}
	if arm["content"] != "Go is a programming language." {
		t.Errorf("content = %v", arm["content"])
	}
	toolUses, ok := arm["toolUses"].([]any)
	if !ok {
		t.Fatalf("toolUses type = %T", arm["toolUses"])
	}
	if len(toolUses) != 1 {
		t.Fatalf("toolUses len = %d", len(toolUses))
	}
}

// ---------------------------------------------------------------------------
// Nullable / Pointer Field Handling
// ---------------------------------------------------------------------------

func TestPointerFields_NilSerializesAsNull(t *testing.T) {
	// ChatCompletionChoice.FinishReason is *string without omitempty → null
	choice := ChatCompletionChoice{Index: 0, FinishReason: nil}
	data, err := json.Marshal(choice)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, exists := raw["finish_reason"]; !exists {
		t.Error("finish_reason should be present as null")
	}
	if raw["finish_reason"] != nil {
		t.Errorf("finish_reason = %v, want nil", raw["finish_reason"])
	}
}

func TestPointerFields_SetValueSerializesCorrectly(t *testing.T) {
	temp := 0.42
	topP := 0.95
	maxTok := 512

	req := ChatCompletionRequest{
		Model:       "gpt-4",
		Messages:    []ChatMessage{{Role: "user", Content: "hi"}},
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   &maxTok,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if raw["temperature"] != 0.42 {
		t.Errorf("temperature = %v, want 0.42", raw["temperature"])
	}
	if raw["top_p"] != 0.95 {
		t.Errorf("top_p = %v, want 0.95", raw["top_p"])
	}
	// JSON numbers are float64
	if raw["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v, want 512", raw["max_tokens"])
	}
}

func TestOmitemptyPointerFields_NilOmitted(t *testing.T) {
	// Temperature, TopP, MaxTokens on ChatCompletionRequest have omitempty
	req := ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, field := range []string{"temperature", "top_p", "max_tokens", "tools", "tool_choice"} {
		if _, exists := raw[field]; exists {
			t.Errorf("field %q should be absent when nil/zero (omitempty)", field)
		}
	}
}

// ---------------------------------------------------------------------------
// Table-Driven: Unknown fields across all model types
// ---------------------------------------------------------------------------

func TestUnknownFields_AllModelTypes(t *testing.T) {
	tests := []struct {
		name   string
		json   string
		target any
	}{
		{
			name:   "ChatCompletionRequest",
			json:   `{"model":"gpt-4","messages":[],"extra_field":"ignored"}`,
			target: &ChatCompletionRequest{},
		},
		{
			name:   "ChatMessage",
			json:   `{"role":"user","content":"hi","extra":true}`,
			target: &ChatMessage{},
		},
		{
			name:   "Tool",
			json:   `{"type":"function","extra_field":123}`,
			target: &Tool{},
		},
		{
			name:   "ToolFunction",
			json:   `{"name":"fn","extra":null}`,
			target: &ToolFunction{},
		},
		{
			name:   "ChatCompletionResponse",
			json:   `{"id":"x","object":"o","created":0,"model":"m","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0},"extra":"val"}`,
			target: &ChatCompletionResponse{},
		},
		{
			name:   "ChatCompletionChoice",
			json:   `{"index":0,"finish_reason":null,"extra":42}`,
			target: &ChatCompletionChoice{},
		},
		{
			name:   "ChatCompletionUsage",
			json:   `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"extra":"x"}`,
			target: &ChatCompletionUsage{},
		},
		{
			name:   "AnthropicMessagesRequest",
			json:   `{"model":"claude","messages":[],"max_tokens":1,"extra":"x"}`,
			target: &AnthropicMessagesRequest{},
		},
		{
			name:   "AnthropicMessage",
			json:   `{"role":"user","content":"hi","extra":true}`,
			target: &AnthropicMessage{},
		},
		{
			name:   "AnthropicTool",
			json:   `{"name":"t","input_schema":{},"extra":"x"}`,
			target: &AnthropicTool{},
		},
		{
			name:   "AnthropicMessagesResponse",
			json:   `{"id":"x","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0},"extra":1}`,
			target: &AnthropicMessagesResponse{},
		},
		{
			name:   "AnthropicUsage",
			json:   `{"input_tokens":0,"output_tokens":0,"extra":"x"}`,
			target: &AnthropicUsage{},
		},
		{
			name:   "KiroPayload",
			json:   `{"conversationState":{"conversationId":"","currentMessage":{"userInputMessage":{"content":""}},"chatTriggerType":"","customizationArn":""},"source":"","streamingFormat":"","extra":"x"}`,
			target: &KiroPayload{},
		},
		{
			name:   "KiroToolResult",
			json:   `{"content":[],"status":"","toolUseId":"","extra":"x"}`,
			target: &KiroToolResult{},
		},
		{
			name:   "ModelInfo",
			json:   `{"modelId":"","maxInputTokens":0,"displayName":"","extra":"x"}`,
			target: &ModelInfo{},
		},
		{
			name:   "ToolSpecification",
			json:   `{"name":"","description":"","inputSchema":{},"extra":"x"}`,
			target: &ToolSpecification{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.json), tt.target); err != nil {
				t.Errorf("unmarshal should not fail on unknown fields: %v", err)
			}
		})
	}
}
