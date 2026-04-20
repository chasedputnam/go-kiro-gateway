// Package models defines the data structures for API requests and responses.
//
// This file contains the Anthropic Messages API models used for request
// parsing and response serialization. All structs use json tags with
// omitempty for optional fields and pointer types for nullable JSON values,
// ensuring correct round-trip serialization (null vs absent).
//
// The Anthropic API has polymorphic fields — system can be a plain string
// or a list of content blocks (for prompt caching), and message content
// can be a string or a list of content blocks (text, image, tool_use,
// tool_result). These are represented as `any` in Go, with the actual
// type determined at runtime during conversion.
package models

// AnthropicMessagesRequest represents an Anthropic Messages API request
// (POST /v1/messages).
//
// System is typed as any because the Anthropic API accepts it as either
// a plain string or a list of content blocks (used for prompt caching).
// ToolChoice is typed as any to accommodate the various tool choice
// strategies: {"type": "auto"}, {"type": "any"}, or {"type": "tool", "name": "..."}.
// Temperature, TopP, and TopK use pointer types so that omitted values
// serialize as absent (not zero), preserving the distinction between
// "not set" and "set to zero".
type AnthropicMessagesRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	System      any                `json:"system,omitempty"` // string | []ContentBlock
	Stream      bool               `json:"stream,omitempty"`
	Tools       []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"` // {"type":"auto"} | {"type":"any"} | {"type":"tool","name":"..."}
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	TopK        *int               `json:"top_k,omitempty"`
}

// AnthropicMessage represents a single message in the Anthropic chat format.
//
// Content is typed as any because it can be a plain string or a list of
// content blocks (text, image, tool_use, tool_result). The actual type
// is determined at runtime during request conversion. Role is typically
// "user" or "assistant".
type AnthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string | []ContentBlock
}

// AnthropicTool represents a tool definition in an Anthropic request.
//
// InputSchema holds the JSON Schema describing the tool's parameters.
// Description is optional but recommended by the Anthropic API for
// better tool selection by the model.
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// AnthropicMessagesResponse is the full non-streaming response from the
// Anthropic Messages API.
//
// Content is a list of content block maps rather than typed structs
// because the blocks can be text, thinking, or tool_use — each with
// different fields. StopReason and StopSequence are pointers so they
// serialize as null when not set, matching the Anthropic API behavior.
type AnthropicMessagesResponse struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Role         string           `json:"role"`
	Content      []map[string]any `json:"content"`
	Model        string           `json:"model"`
	StopReason   *string          `json:"stop_reason"`
	StopSequence *string          `json:"stop_sequence"`
	Usage        AnthropicUsage   `json:"usage"`
}

// AnthropicUsage reports token consumption for an Anthropic Messages
// API response. InputTokens is the number of tokens in the request,
// and OutputTokens is the number of tokens generated in the response.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
