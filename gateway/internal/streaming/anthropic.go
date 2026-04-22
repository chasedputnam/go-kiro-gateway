// Package streaming — Anthropic SSE formatter.
//
// StreamToAnthropic consumes a KiroEvent channel (produced by ParseKiroStream)
// and writes Anthropic-compatible SSE events to an http.ResponseWriter.
//
// Each event is formatted as:
//
//	event: {type}\ndata: {json}\n\n
//
// The event sequence follows the Anthropic Messages streaming specification:
//
//	message_start → content_block_start → content_block_delta* →
//	content_block_stop → ... → message_delta → message_stop
package streaming

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/parser"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/tokenizer"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Anthropic SSE formatter
// ---------------------------------------------------------------------------

// AnthropicStreamOptions configures the Anthropic SSE formatter.
type AnthropicStreamOptions struct {
	// Model is the model name included in the message_start event.
	Model string

	// ThinkingHandlingMode controls how thinking content is emitted.
	ThinkingHandlingMode thinking.HandlingMode

	// MaxInputTokens is the model's max input token limit, used for
	// prompt token calculation from context usage percentage.
	MaxInputTokens int

	// InputTokens is the pre-calculated input token count from the request.
	InputTokens int
}

// GenerateMessageID returns a unique message ID in the format "msg_{hex24}".
func GenerateMessageID() string {
	return "msg_" + uuid.New().String()[:24]
}

// GenerateToolUseID returns a unique tool use ID in the format "toolu_{hex24}".
func GenerateToolUseID() string {
	return "toolu_" + uuid.New().String()[:24]
}

// formatSSEEvent formats a single Anthropic SSE event.
func formatSSEEvent(eventType string, data map[string]any) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, jsonData)), nil
}

// writeSSEEvent writes a formatted SSE event to the response writer.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data map[string]any) {
	raw, err := formatSSEEvent(eventType, data)
	if err != nil {
		log.Printf("Failed to marshal Anthropic event %q: %v", eventType, err)
		return
	}
	w.Write(raw)
	flusher.Flush()
}

// StreamToAnthropic reads events from the channel and writes Anthropic SSE
// events to w. The caller must have already set appropriate headers on w
// before calling this function. The function returns when the channel is
// closed. The returned slice contains any tool calls that were truncated by
// the upstream API (IsTruncated == true), so callers can save them for
// recovery on the next request.
func StreamToAnthropic(w http.ResponseWriter, events <-chan KiroEvent, opts AnthropicStreamOptions) []ToolCallInfo {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	messageID := GenerateMessageID()
	inputTokens := opts.InputTokens

	// Send message_start.
	writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         opts.Model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	})

	// Block tracking state.
	var (
		currentBlockIndex    int
		thinkingBlockStarted bool
		thinkingBlockIndex   int
		textBlockStarted     bool
		textBlockIndex       int

		fullContent         string
		fullThinkingContent string
		contextUsagePct     float64
		toolCallsFromStream []ToolCallInfo
	)

	// closeThinkingBlock emits content_block_stop for the thinking block.
	closeThinkingBlock := func() {
		if !thinkingBlockStarted {
			return
		}
		writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": thinkingBlockIndex,
		})
		thinkingBlockStarted = false
		currentBlockIndex++
	}

	// closeTextBlock emits content_block_stop for the text block.
	closeTextBlock := func() {
		if !textBlockStarted {
			return
		}
		writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": textBlockIndex,
		})
		textBlockStarted = false
		currentBlockIndex++
	}

	// ensureTextBlock starts a text content block if not already started.
	ensureTextBlock := func() {
		if textBlockStarted {
			return
		}
		textBlockIndex = currentBlockIndex
		writeSSEEvent(w, flusher, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": textBlockIndex,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		})
		textBlockStarted = true
	}

	for event := range events {
		switch event.Type {
		case EventTypeContent:
			if event.Content == "" && event.ContextUsagePercentage == 0 {
				continue
			}

			// Track context usage percentage.
			if event.ContextUsagePercentage > 0 {
				contextUsagePct = event.ContextUsagePercentage
				continue
			}

			fullContent += event.Content

			// Close thinking block if transitioning to regular content.
			closeThinkingBlock()

			// Start text block if needed.
			ensureTextBlock()

			// Send content delta.
			writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textBlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": event.Content,
				},
			})

		case EventTypeThinking:
			if event.ThinkingContent == "" {
				continue
			}

			fullThinkingContent += event.ThinkingContent

			if opts.ThinkingHandlingMode == thinking.AsReasoningContent {
				// Start thinking block if not started.
				if !thinkingBlockStarted {
					thinkingBlockIndex = currentBlockIndex
					writeSSEEvent(w, flusher, "content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": thinkingBlockIndex,
						"content_block": map[string]any{
							"type":      "thinking",
							"thinking":  "",
							"signature": generateThinkingSignature(),
						},
					})
					thinkingBlockStarted = true
				}

				// Send thinking delta.
				writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": thinkingBlockIndex,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": event.ThinkingContent,
					},
				})
			}
			// For other modes, thinking content is either passed as text or dropped.

		case EventTypeToolCall:
			if event.ToolCall != nil {
				toolCallsFromStream = append(toolCallsFromStream, *event.ToolCall)
			}

		case EventTypeUsage:
			// Anthropic format doesn't use credits in the same way; skip.

		case EventTypeError:
			log.Printf("Error during Anthropic streaming: %v", event.Error)
			return nil

		case EventTypeDone:
			// Will be handled after the loop.
		}
	}

	// --- Post-stream processing ---

	// Parse bracket-style tool calls from accumulated content.
	bracketCalls := parser.ParseBracketToolCalls(fullContent)
	allToolCalls := mergeAndDeduplicateToolCalls(toolCallsFromStream, bracketCalls)

	// Close thinking block if still open.
	closeThinkingBlock()

	// Close text block if still open.
	closeTextBlock()

	// Emit tool_use content blocks.
	for _, tc := range allToolCalls {
		toolID := tc.ID
		if toolID == "" {
			toolID = GenerateToolUseID()
		}

		// Parse arguments to a map for Anthropic format.
		var toolInput map[string]any
		if err := json.Unmarshal([]byte(tc.Arguments), &toolInput); err != nil {
			toolInput = map[string]any{}
		}

		// content_block_start for tool_use.
		writeSSEEvent(w, flusher, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": currentBlockIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    toolID,
				"name":  tc.Name,
				"input": map[string]any{},
			},
		})

		// Send tool input as input_json_delta.
		inputJSON, _ := json.Marshal(toolInput)
		writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": currentBlockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": string(inputJSON),
			},
		})

		// content_block_stop.
		writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": currentBlockIndex,
		})

		currentBlockIndex++
	}

	// Calculate output tokens.
	outputTokens := tokenizer.CountTokens(fullContent + fullThinkingContent)

	// Recalculate input tokens from context usage if available.
	if contextUsagePct > 0 && opts.MaxInputTokens > 0 {
		inputTokens = tokenizer.CalculatePromptTokens(outputTokens, contextUsagePct/100, opts.MaxInputTokens)
	}

	// Determine stop reason.
	stopReason := "end_turn"
	if len(allToolCalls) > 0 {
		stopReason = "tool_use"
	}

	// Send message_delta with stop_reason and usage.
	writeSSEEvent(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": outputTokens,
		},
	})

	// Send message_stop.
	writeSSEEvent(w, flusher, "message_stop", map[string]any{
		"type": "message_stop",
	})

	// Collect truncated tool calls for the caller to save to truncation state.
	var truncated []ToolCallInfo
	for _, tc := range allToolCalls {
		if tc.IsTruncated {
			truncated = append(truncated, tc)
		}
	}
	return truncated
}

// generateThinkingSignature returns a placeholder signature for thinking
// content blocks. In the real Anthropic API this is a cryptographic
// signature; since we use fake reasoning via tag injection, we generate
// a placeholder.
func generateThinkingSignature() string {
	return "sig_" + uuid.New().String()[:32]
}
