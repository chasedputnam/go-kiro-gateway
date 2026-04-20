// Package models defines the data structures for API requests and responses.
//
// This file contains the Kiro API payload models used for constructing
// requests to the upstream Kiro (Amazon Q Developer) API. These structs
// represent the internal wire format that the gateway translates OpenAI
// and Anthropic requests into.
//
// The Kiro API uses a conversation-based format with userInputMessage
// and assistantResponseMessage history entries, a currentMessage for the
// latest user input, and optional tool specifications. This differs from
// both the OpenAI and Anthropic formats, requiring translation in the
// converter layer.
//
// HistoryMessage is typed as map[string]any because each entry is either
// {"userInputMessage": {...}} or {"assistantResponseMessage": {...}},
// and Go's type system does not natively express tagged unions. The
// converter layer constructs these maps with the correct keys.
package models

// KiroPayload is the top-level request body sent to the Kiro
// generateAssistantResponse API endpoint.
//
// ConversationState carries the full conversation context including
// history and the current user message. ProfileARN is included only
// for Kiro Desktop auth (omitted for AWS SSO OIDC). Source identifies
// the caller, and StreamingFormat specifies the response encoding.
type KiroPayload struct {
	ConversationState ConversationState `json:"conversationState"`
	ProfileARN        string            `json:"profileArn,omitempty"`
	Source            string            `json:"source"`
	StreamingFormat   string            `json:"streamingFormat"`
}

// ConversationState holds the full conversation context for a Kiro API
// request, including the conversation identifier, the current message
// being sent, the trigger type, an optional customization ARN, and the
// conversation history.
//
// History contains previous messages in alternating
// userInputMessage / assistantResponseMessage format. It is omitted
// when the conversation has no prior turns.
type ConversationState struct {
	ConversationID   string           `json:"conversationId"`
	CurrentMessage   CurrentMessage   `json:"currentMessage"`
	ChatTriggerType  string           `json:"chatTriggerType"`
	CustomizationARN string           `json:"customizationArn"`
	History          []HistoryMessage `json:"history,omitempty"`
}

// CurrentMessage wraps the latest user input and its optional context
// (tool results from previous assistant tool calls).
//
// UserInputMessageContext is a pointer so it is omitted from JSON when
// there are no tool results to include.
type CurrentMessage struct {
	UserInputMessage        UserInputMessage         `json:"userInputMessage"`
	UserInputMessageContext *UserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

// UserInputMessage represents the content of a user's message in Kiro
// format. Content is the text body, and Images holds any vision inputs
// (base64-encoded image data).
type UserInputMessage struct {
	Content string      `json:"content"`
	Images  []KiroImage `json:"images,omitempty"`
}

// UserInputMessageContext carries additional context alongside a user
// message, specifically tool results from prior assistant tool calls.
// This is separate from the message content because Kiro API treats
// tool results as structured metadata rather than inline content.
type UserInputMessageContext struct {
	ToolResults []KiroToolResult `json:"toolResults,omitempty"`
}

// KiroImage represents a base64-encoded image attached to a user
// message. Format is the image MIME subtype (e.g., "jpeg", "png",
// "gif", "webp") and Source contains the raw bytes.
type KiroImage struct {
	Format string          `json:"format"`
	Source KiroImageSource `json:"source"`
}

// KiroImageSource holds the base64-encoded bytes of an image. The
// Kiro API expects image data as a base64 string without the data URL
// prefix (no "data:image/png;base64," header).
type KiroImageSource struct {
	Bytes string `json:"bytes"`
}

// KiroToolResult represents the result of a tool invocation returned
// by the client. Content holds one or more text blocks with the tool
// output, Status indicates success or failure ("success" / "error"),
// and ToolUseID links the result back to the original tool call.
type KiroToolResult struct {
	Content   []KiroTextContent `json:"content"`
	Status    string            `json:"status"`
	ToolUseID string            `json:"toolUseId"`
}

// KiroTextContent is a simple text content block used inside tool
// results and other Kiro API structures.
type KiroTextContent struct {
	Text string `json:"text"`
}

// HistoryMessage represents a single entry in the conversation history
// sent to the Kiro API.
//
// Each history entry is either a user message or an assistant message,
// expressed as a JSON object with one of two possible keys:
//
//	{"userInputMessage": {"content": "...", "images": [...]}}
//	{"assistantResponseMessage": {"content": "...", "toolUses": [...]}}
//
// Because Go does not have native tagged unions, HistoryMessage is
// defined as map[string]any. The converter layer is responsible for
// constructing these maps with the correct structure. Using a map
// preserves the exact JSON shape that the Kiro API expects without
// requiring wrapper structs or custom marshalers.
type HistoryMessage = map[string]any

// ToolSpecification describes a tool definition sent to the Kiro API
// as part of the request payload. The converter layer translates
// OpenAI and Anthropic tool formats into this structure.
//
// Name is the tool identifier (max 64 characters per Kiro API
// validation). Description explains the tool's purpose to the model.
// InputSchema is the JSON Schema describing the tool's parameters.
type ToolSpecification struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ModelInfo holds metadata about a model available through the Kiro
// API. It is used as the value type in the model metadata cache.
//
// ModelID is the internal Kiro model identifier (e.g.,
// "claude-sonnet-4"). MaxInputTokens is the model's context window
// size used for context usage percentage calculations. DisplayName
// is the human-readable name shown in /v1/models responses.
type ModelInfo struct {
	ModelID        string `json:"modelId"`
	MaxInputTokens int    `json:"maxInputTokens"`
	DisplayName    string `json:"displayName"`
}
