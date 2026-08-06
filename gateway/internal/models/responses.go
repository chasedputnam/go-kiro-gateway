// Package models — OpenAI Responses API data structures.
//
// This file defines the request and response types for the OpenAI Responses
// API (POST /v1/responses). The Responses API uses an `input` array instead
// of `messages` and returns an `output` array of typed items instead of
// `choices`.
//
// Reference: https://platform.openai.com/docs/api-reference/responses
package models

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// ResponsesRequest represents an OpenAI Responses API request body.
//
// Input is typed as any because it can be either a plain string or an array
// of InputItem objects. The handler unmarshals and inspects the value at
// runtime.
type ResponsesRequest struct {
	Model              string          `json:"model"`
	Input              any             `json:"input"`                         // string or []InputItem
	Instructions       string          `json:"instructions,omitempty"`        // system prompt
	Tools              []ResponsesTool `json:"tools,omitempty"`
	Stream             bool            `json:"stream,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Reasoning          *ReasoningConfig `json:"reasoning,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"` // ignored by gateway (stateless)
	Metadata           map[string]any  `json:"metadata,omitempty"`
}

// InputItem is a single item in the Responses API `input` array.
//
// Type is one of:
//   - "message"               — a conversational turn (user or assistant)
//   - "function_call"         — an assistant-side tool invocation from history
//   - "function_call_output"  — the result of a prior tool call
//
// Fields are shared across types; only the relevant subset is populated for
// each type value.
type InputItem struct {
	Type string `json:"type"` // "message", "function_call", "function_call_output"

	// message fields
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"` // string or []ContentBlock

	// function_call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output fields
	// CallID is shared with function_call above.
	Output string `json:"output,omitempty"`
}

// ContentBlock is a typed content block within a message's content array.
//
// Type is one of: "input_text", "output_text", "input_image", "refusal".
type ContentBlock struct {
	Type     string         `json:"type"` // "input_text", "output_text", "input_image", "refusal"
	Text     string         `json:"text,omitempty"`
	ImageURL *ImageURLBlock `json:"image_url,omitempty"`
	Detail   string         `json:"detail,omitempty"`
}

// ImageURLBlock holds an image URL (data URI or http/https) in a content block.
type ImageURLBlock struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ResponsesTool defines a tool that the model may call.
// Only type "function" is supported.
type ResponsesTool struct {
	Type        string         `json:"type"` // "function"
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// ReasoningConfig controls the model's internal reasoning behaviour.
// The gateway accepts this field but does not forward it to Kiro — thinking
// is controlled by FAKE_REASONING settings instead.
type ReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`  // "low", "medium", "high"
	Summary string `json:"summary,omitempty"` // "auto", "concise", "detailed"
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// ResponsesResponse is the top-level non-streaming response for POST /v1/responses.
type ResponsesResponse struct {
	ID        string          `json:"id"`         // "resp_<uuid>"
	Object    string          `json:"object"`     // "response"
	CreatedAt int64           `json:"created_at"` // Unix timestamp
	Model     string          `json:"model"`
	Status    string          `json:"status"` // "completed", "failed", "in_progress"
	Output    []OutputItem    `json:"output"`
	Usage     *ResponsesUsage `json:"usage,omitempty"`
	Error     *ResponsesError `json:"error,omitempty"`
}

// OutputItem is a single item in the Responses API `output` array.
//
// Type is one of: "message", "function_call", "reasoning".
// Only the relevant fields are populated for each type.
type OutputItem struct {
	Type string `json:"type"` // "message", "function_call", "reasoning"
	ID   string `json:"id"`

	// message fields
	Role    string          `json:"role,omitempty"`
	Content []OutputContent `json:"content,omitempty"`
	Status  string          `json:"status,omitempty"` // "completed", "in_progress"

	// function_call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// reasoning fields
	Summary []SummaryContent `json:"summary,omitempty"`
}

// OutputContent is a typed content block within an output message item.
type OutputContent struct {
	Type        string `json:"type"`                  // "output_text", "refusal"
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

// SummaryContent is a typed block within a reasoning item's summary array.
type SummaryContent struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text"`
}

// ResponsesUsage reports token consumption for a Responses API call.
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponsesError holds error detail for a failed Responses API response.
type ResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
