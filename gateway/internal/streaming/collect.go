// Package streaming — non-streaming response collection.
//
// CollectFullResponse consumes a KiroEvent channel and accumulates all
// content, thinking content, tool calls, and usage data into a single
// CollectedResponse struct. Callers then use BuildOpenAIResponse or
// BuildAnthropicResponse to produce the final JSON object.
package streaming

import (
	"encoding/json"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/parser"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/tokenizer"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// CollectedResponse
// ---------------------------------------------------------------------------

// CollectedResponse holds the accumulated data from a fully consumed event
// stream. It is the intermediate representation used to build format-specific
// (OpenAI or Anthropic) non-streaming JSON responses.
type CollectedResponse struct {
	// Content is the accumulated text content.
	Content string

	// ThinkingContent is the accumulated reasoning/thinking content.
	ThinkingContent string

	// ToolCalls contains all deduplicated tool calls.
	ToolCalls []ToolCallInfo

	// TruncatedToolCalls contains tool calls that were truncated by the
	// upstream API. Callers should save these to truncation state so the
	// next request can inject recovery notices.
	TruncatedToolCalls []ToolCallInfo

	// ContextUsagePercentage is the context usage percentage from the
	// Kiro API, or 0 if not reported.
	ContextUsagePercentage float64

	// Credits is the credit usage from the Kiro API, or nil if not reported.
	Credits *float64
}

// CollectFullResponse consumes all events from the channel and returns a
// CollectedResponse. It handles bracket tool call parsing and deduplication.
func CollectFullResponse(events <-chan KiroEvent) *CollectedResponse {
	resp := &CollectedResponse{}
	var toolCallsFromStream []ToolCallInfo

	for event := range events {
		switch event.Type {
		case EventTypeContent:
			if event.Content != "" {
				resp.Content += event.Content
			}
			if event.ContextUsagePercentage > 0 {
				resp.ContextUsagePercentage = event.ContextUsagePercentage
			}

		case EventTypeThinking:
			if event.ThinkingContent != "" {
				resp.ThinkingContent += event.ThinkingContent
			}

		case EventTypeToolCall:
			if event.ToolCall != nil {
				toolCallsFromStream = append(toolCallsFromStream, *event.ToolCall)
			}

		case EventTypeUsage:
			if event.Usage != nil {
				credits := event.Usage.Credits
				resp.Credits = &credits
			}

		case EventTypeError:
			// Errors in non-streaming mode are typically handled by the caller.
			// We stop collecting.
			return resp

		case EventTypeDone:
			// Normal end of stream.
		}
	}

	// Parse bracket-style tool calls from accumulated content.
	bracketCalls := parser.ParseBracketToolCalls(resp.Content)
	resp.ToolCalls = mergeAndDeduplicateToolCalls(toolCallsFromStream, bracketCalls)

	// Collect truncated tool calls for the caller to save to truncation state.
	for _, tc := range resp.ToolCalls {
		if tc.IsTruncated {
			resp.TruncatedToolCalls = append(resp.TruncatedToolCalls, tc)
		}
	}

	return resp
}

// ---------------------------------------------------------------------------
// OpenAI non-streaming response builder
// ---------------------------------------------------------------------------

// OpenAINonStreamOptions configures the OpenAI non-streaming response builder.
type OpenAINonStreamOptions struct {
	// Model is the model name included in the response.
	Model string

	// ThinkingHandlingMode controls how thinking content appears in the response.
	ThinkingHandlingMode thinking.HandlingMode

	// MaxInputTokens is the model's max input token limit.
	MaxInputTokens int
}

// BuildOpenAIResponse constructs a complete OpenAI chat.completion JSON
// object from a CollectedResponse.
func BuildOpenAIResponse(resp *CollectedResponse, opts OpenAINonStreamOptions) map[string]any {
	completionID := GenerateCompletionID()
	createdTime := time.Now().Unix()

	// Build message.
	message := map[string]any{
		"role":    "assistant",
		"content": resp.Content,
	}

	// Add reasoning_content if present and mode is as_reasoning_content.
	if resp.ThinkingContent != "" && opts.ThinkingHandlingMode == thinking.AsReasoningContent {
		message["reasoning_content"] = resp.ThinkingContent
	}

	// Add tool calls (without streaming index field).
	if len(resp.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			id := tc.ID
			if id == "" {
				id = GenerateToolCallID()
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		message["tool_calls"] = toolCalls
	}

	// Determine finish_reason.
	finishReason := "stop"
	if len(resp.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	// Calculate token usage.
	completionTokens := tokenizer.CountTokens(resp.Content + resp.ThinkingContent)
	promptTokens := 0
	totalTokens := completionTokens

	if resp.ContextUsagePercentage > 0 && opts.MaxInputTokens > 0 {
		promptTokens = tokenizer.CalculatePromptTokens(completionTokens, resp.ContextUsagePercentage/100, opts.MaxInputTokens)
		totalTokens = promptTokens + completionTokens
	}

	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	if resp.Credits != nil {
		usage["credits_used"] = *resp.Credits
	}

	return map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": createdTime,
		"model":   opts.Model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": usage,
	}
}

// ---------------------------------------------------------------------------
// Anthropic non-streaming response builder
// ---------------------------------------------------------------------------

// AnthropicNonStreamOptions configures the Anthropic non-streaming response
// builder.
type AnthropicNonStreamOptions struct {
	// Model is the model name included in the response.
	Model string

	// ThinkingHandlingMode controls how thinking content appears in the response.
	ThinkingHandlingMode thinking.HandlingMode

	// MaxInputTokens is the model's max input token limit.
	MaxInputTokens int

	// InputTokens is the pre-calculated input token count.
	InputTokens int
}

// BuildAnthropicResponse constructs a complete Anthropic message JSON
// object from a CollectedResponse.
func BuildAnthropicResponse(resp *CollectedResponse, opts AnthropicNonStreamOptions) map[string]any {
	messageID := "msg_" + uuid.New().String()[:24]
	inputTokens := opts.InputTokens

	// Build content blocks.
	var contentBlocks []map[string]any

	// Add thinking block first if present and mode is as_reasoning_content.
	if resp.ThinkingContent != "" && opts.ThinkingHandlingMode == thinking.AsReasoningContent {
		contentBlocks = append(contentBlocks, map[string]any{
			"type":      "thinking",
			"thinking":  resp.ThinkingContent,
			"signature": generateThinkingSignature(),
		})
	}

	// Add text block if there's content.
	if resp.Content != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": resp.Content,
		})
	}

	// Add tool_use blocks.
	for _, tc := range resp.ToolCalls {
		toolID := tc.ID
		if toolID == "" {
			toolID = GenerateToolUseID()
		}

		var toolInput map[string]any
		if err := json.Unmarshal([]byte(tc.Arguments), &toolInput); err != nil {
			toolInput = map[string]any{}
		}

		contentBlocks = append(contentBlocks, map[string]any{
			"type":  "tool_use",
			"id":    toolID,
			"name":  tc.Name,
			"input": toolInput,
		})
	}

	// Calculate output tokens.
	outputTokens := tokenizer.CountTokens(resp.Content + resp.ThinkingContent)

	// Recalculate input tokens from context usage if available.
	if resp.ContextUsagePercentage > 0 && opts.MaxInputTokens > 0 {
		inputTokens = tokenizer.CalculatePromptTokens(outputTokens, resp.ContextUsagePercentage/100, opts.MaxInputTokens)
	}

	// Determine stop reason.
	stopReason := "end_turn"
	if len(resp.ToolCalls) > 0 {
		stopReason = "tool_use"
	}

	return map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"content":       contentBlocks,
		"model":         opts.Model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
}
