// Package models defines the data structures for API requests and responses.
//
// This file contains the OpenAI-compatible Chat Completions API models used
// for request parsing and response serialization. All structs use json tags
// with omitempty for optional fields and pointer types for nullable JSON
// values, ensuring correct round-trip serialization (null vs absent).
package models

// ChatCompletionRequest represents an OpenAI Chat Completions API request.
//
// Only the fields that the gateway actively uses are defined as typed fields.
// Unknown fields are silently ignored by encoding/json, providing forward
// compatibility with new OpenAI API additions.
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Tools       []Tool        `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
}

// ChatMessage represents a single message in the OpenAI chat format.
//
// Content is typed as any because it can be a plain string or a list of
// content blocks (text, image_url, etc.). ToolCalls uses []any to accept
// the varied tool call structures without strict typing at the model layer.
type ChatMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCalls  []any  `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Tool represents a tool definition in an OpenAI request.
//
// Two formats are supported:
//  1. Standard OpenAI: {"type": "function", "function": {"name": "...", ...}}
//  2. Cursor flat:     {"type": "function", "name": "...", "description": "...", "input_schema": {...}}
//
// The flat format fields (Name, Description, InputSchema) are populated when
// clients like Cursor send tool definitions without the nested function object.
type Tool struct {
	Type     string        `json:"type"`
	Function *ToolFunction `json:"function,omitempty"`

	// Cursor flat format fields.
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// ToolFunction describes a callable function within a Tool definition.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatCompletionResponse is the full non-streaming response for a chat
// completion request.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

// ChatCompletionChoice represents a single completion variant.
//
// Message is used in non-streaming responses; Delta is used in streaming
// chunks. Both are map[string]any to accommodate the dynamic shape of
// assistant messages (content, tool_calls, reasoning_content, etc.).
// FinishReason is a pointer so that it serializes as null (not absent)
// when no finish reason has been determined yet.
type ChatCompletionChoice struct {
	Index        int            `json:"index"`
	Message      map[string]any `json:"message,omitempty"`
	Delta        map[string]any `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

// ChatCompletionUsage reports token consumption for a completion.
//
// CreditsUsed is a Kiro-specific extension; it is omitted from the JSON
// when nil.
type ChatCompletionUsage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	CreditsUsed      *float64 `json:"credits_used,omitempty"`
}

// StreamingChunk is a type alias for ChatCompletionResponse used when
// building individual SSE chunks during streaming. The structure is
// identical — the alias exists for readability at call sites that
// distinguish between full responses and incremental chunks.
type StreamingChunk = ChatCompletionResponse
