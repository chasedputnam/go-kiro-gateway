// Package streaming — OpenAI SSE formatter.
//
// StreamToOpenAI consumes a KiroEvent channel (produced by ParseKiroStream)
// and writes OpenAI-compatible SSE chunks to an http.ResponseWriter.
//
// Each chunk is formatted as:
//
//	data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1234,"model":"...","choices":[...]}\n\n
//
// The final events are a chunk with finish_reason + usage, followed by:
//
//	data: [DONE]\n\n
package streaming

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/parser"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/tokenizer"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// OpenAI SSE formatter
// ---------------------------------------------------------------------------

// OpenAIStreamOptions configures the OpenAI SSE formatter.
type OpenAIStreamOptions struct {
	// Model is the model name included in every chunk.
	Model string

	// ThinkingHandlingMode controls how thinking content is emitted.
	ThinkingHandlingMode thinking.HandlingMode

	// MaxInputTokens is the model's max input token limit, used for
	// prompt token calculation from context usage percentage.
	MaxInputTokens int

	// RequestMessages are the original request messages for fallback
	// prompt token estimation when context usage percentage is unavailable.
	RequestMessages []map[string]any

	// RequestTools are the original request tools for fallback prompt
	// token estimation.
	RequestTools []map[string]any
}

// GenerateCompletionID returns a unique completion ID in the format
// "chatcmpl-{uuid}".
func GenerateCompletionID() string {
	return "chatcmpl-" + uuid.New().String()
}

// GenerateToolCallID returns a unique tool call ID in the format
// "call_{hex24}".
func GenerateToolCallID() string {
	return "call_" + uuid.New().String()[:24]
}

// StreamToOpenAI reads events from the channel and writes OpenAI SSE chunks
// to w. The caller must have already set appropriate headers on w before
// calling this function. The function returns when the channel is closed.
// The returned slice contains any tool calls that were truncated by the
// upstream API (IsTruncated == true), so callers can save them for recovery
// on the next request.
func StreamToOpenAI(w http.ResponseWriter, events <-chan KiroEvent, opts OpenAIStreamOptions) []ToolCallInfo {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	completionID := GenerateCompletionID()
	createdTime := time.Now().Unix()
	firstChunk := true

	var (
		fullContent         string
		fullThinkingContent string
		contextUsagePct     float64
		usageCredits        *float64
		toolCallsFromStream []ToolCallInfo
	)

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

			delta := map[string]any{"content": event.Content}
			if firstChunk {
				delta["role"] = "assistant"
				firstChunk = false
			}

			writeOpenAIChunk(w, flusher, completionID, createdTime, opts.Model, delta, nil)

		case EventTypeThinking:
			if event.ThinkingContent == "" {
				continue
			}

			fullThinkingContent += event.ThinkingContent

			var delta map[string]any
			if opts.ThinkingHandlingMode == thinking.AsReasoningContent {
				delta = map[string]any{"reasoning_content": event.ThinkingContent}
			} else {
				delta = map[string]any{"content": event.ThinkingContent}
			}

			if firstChunk {
				delta["role"] = "assistant"
				firstChunk = false
			}

			writeOpenAIChunk(w, flusher, completionID, createdTime, opts.Model, delta, nil)

		case EventTypeToolCall:
			if event.ToolCall != nil {
				toolCallsFromStream = append(toolCallsFromStream, *event.ToolCall)
			}

		case EventTypeUsage:
			if event.Usage != nil {
				credits := event.Usage.Credits
				usageCredits = &credits
			}

		case EventTypeError:
			log.Printf("Error during OpenAI streaming: %v", event.Error)
			return nil

		case EventTypeDone:
			// Will be handled after the loop.
		}
	}

	// --- Post-stream processing ---

	// Parse bracket-style tool calls from accumulated content.
	bracketCalls := parser.ParseBracketToolCalls(fullContent)
	allToolCalls := mergeAndDeduplicateToolCalls(toolCallsFromStream, bracketCalls)

	// Determine finish_reason.
	finishReason := "stop"
	if len(allToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	// Send tool calls chunk if present.
	if len(allToolCalls) > 0 {
		indexedToolCalls := make([]map[string]any, 0, len(allToolCalls))
		for idx, tc := range allToolCalls {
			id := tc.ID
			if id == "" {
				id = GenerateToolCallID()
			}
			indexedToolCalls = append(indexedToolCalls, map[string]any{
				"index": idx,
				"id":    id,
				"type":  "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		delta := map[string]any{"tool_calls": indexedToolCalls}
		writeOpenAIChunk(w, flusher, completionID, createdTime, opts.Model, delta, nil)
	}

	// Calculate token usage.
	completionTokens := tokenizer.CountTokens(fullContent + fullThinkingContent)
	promptTokens := 0
	totalTokens := completionTokens

	if contextUsagePct > 0 && opts.MaxInputTokens > 0 {
		promptTokens = tokenizer.CalculatePromptTokens(completionTokens, contextUsagePct/100, opts.MaxInputTokens)
		totalTokens = promptTokens + completionTokens
	}

	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	if usageCredits != nil {
		usage["credits_used"] = *usageCredits
	}

	// Send final chunk with finish_reason and usage.
	writeOpenAIFinalChunk(w, flusher, completionID, createdTime, opts.Model, finishReason, usage)

	// Send [DONE].
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// Collect truncated tool calls for the caller to save to truncation state.
	var truncated []ToolCallInfo
	for _, tc := range allToolCalls {
		if tc.IsTruncated {
			truncated = append(truncated, tc)
		}
	}
	return truncated
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeOpenAIChunk writes a single SSE chunk with the given delta.
func writeOpenAIChunk(
	w http.ResponseWriter,
	flusher http.Flusher,
	id string,
	created int64,
	model string,
	delta map[string]any,
	finishReason *string,
) {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		log.Printf("Failed to marshal OpenAI chunk: %v", err)
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// writeOpenAIFinalChunk writes the final chunk with finish_reason and usage.
func writeOpenAIFinalChunk(
	w http.ResponseWriter,
	flusher http.Flusher,
	id string,
	created int64,
	model string,
	finishReason string,
	usage map[string]any,
) {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
		"usage": usage,
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		log.Printf("Failed to marshal OpenAI final chunk: %v", err)
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// mergeAndDeduplicateToolCalls combines structured tool calls from the stream
// with bracket-style tool calls parsed from text, then deduplicates.
func mergeAndDeduplicateToolCalls(streamCalls []ToolCallInfo, bracketCalls []parser.BracketToolCall) []ToolCallInfo {
	if len(bracketCalls) == 0 {
		return streamCalls
	}

	// Convert bracket calls to ToolCallInfo.
	all := make([]ToolCallInfo, 0, len(streamCalls)+len(bracketCalls))
	all = append(all, streamCalls...)

	for _, bc := range bracketCalls {
		all = append(all, ToolCallInfo{
			Name:      bc.Name,
			Arguments: bc.Arguments,
		})
	}

	return deduplicateToolCalls(all)
}
