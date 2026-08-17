// Package streaming — OpenAI Responses API SSE formatter and non-streaming builder.
//
// StreamToResponses consumes a KiroEvent channel (produced by ParseKiroStream)
// and writes Responses API-compatible SSE events to an http.ResponseWriter.
//
// The event sequence follows the OpenAI Responses API streaming specification:
//
//	response.created
//	response.output_item.added       (reasoning, if any)
//	response.output_item.done        (reasoning)
//	response.output_item.added       (message)
//	response.content_part.added
//	response.output_text.delta*
//	response.output_text.done
//	response.output_item.done        (message)
//	response.output_item.added       (function_call, per tool)
//	response.function_call_arguments.delta*
//	response.function_call_arguments.done
//	response.output_item.done        (function_call)
//	response.completed
//
// BuildResponsesResponse constructs the complete non-streaming JSON object
// from a CollectedResponse.
package streaming

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/parser"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/thinking"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/tokenizer"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// ID generation helpers
// ---------------------------------------------------------------------------

// GenerateResponseID returns a unique response ID: "resp_<uuid24>".
func GenerateResponseID() string {
	return "resp_" + uuid.New().String()[:24]
}

// generateOutputItemID generates an ID for a response output item with the
// given prefix (e.g. "msg_", "fc_", "rs_").
func generateOutputItemID(prefix string) string {
	return prefix + uuid.New().String()[:24]
}

// ---------------------------------------------------------------------------
// ResponsesStreamOptions
// ---------------------------------------------------------------------------

// ResponsesStreamOptions configures the Responses API SSE formatter.
type ResponsesStreamOptions struct {
	// Model is the model name included in every event.
	Model string

	// ThinkingHandlingMode controls how thinking content is emitted.
	ThinkingHandlingMode thinking.HandlingMode

	// MaxInputTokens is the model's max input token limit.
	MaxInputTokens int

	// InputTokens is the pre-calculated input token count.
	InputTokens int

	// CustomToolNames identifies tools whose Kiro JSON arguments must be
	// restored to Responses custom_tool_call free-form input events.
	CustomToolNames map[string]bool
}

// ResponsesNonStreamOptions configures the non-streaming response builder.
type ResponsesNonStreamOptions struct {
	Model           string
	MaxInputTokens  int
	InputTokens     int
	CustomToolNames map[string]bool
}

// ---------------------------------------------------------------------------
// writeResponsesEvent — SSE event writer helper
// ---------------------------------------------------------------------------

// writeResponsesEvent writes a single Responses API SSE event with the form:
//
//	event: {eventType}\ndata: {json}\n\n
func writeResponsesEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data map[string]any) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Error().Err(err).Str("event_type", eventType).Msg("Failed to marshal Responses SSE event")
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
	flusher.Flush()
}

func isResponsesCustomTool(name string, customToolNames map[string]bool) bool {
	return customToolNames != nil && customToolNames[name]
}

func customToolInput(arguments string) string {
	var wrapped map[string]any
	if err := json.Unmarshal([]byte(arguments), &wrapped); err != nil {
		return arguments
	}
	if input, ok := wrapped["input"].(string); ok {
		return input
	}
	return arguments
}

func responsesToolCallItem(itemID, callID, name, arguments string, custom bool) map[string]any {
	if custom {
		return map[string]any{
			"type":    "custom_tool_call",
			"id":      itemID,
			"call_id": callID,
			"name":    name,
			"input":   customToolInput(arguments),
		}
	}
	return map[string]any{
		"type":      "function_call",
		"id":        itemID,
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
	}
}

func responsesToolCallItemID(custom bool) string {
	if custom {
		return generateOutputItemID("ctc_")
	}
	return generateOutputItemID("fc_")
}

// ---------------------------------------------------------------------------
// StreamToResponses
// ---------------------------------------------------------------------------

// StreamToResponses reads events from the channel and writes Responses API
// SSE events to w. It returns the list of truncated tool calls (same contract
// as StreamToOpenAI) so callers can save them to truncation state.
func StreamToResponses(w http.ResponseWriter, events <-chan KiroEvent, opts ResponsesStreamOptions) []ToolCallInfo {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	responseID := GenerateResponseID()
	createdAt := time.Now().Unix()

	// Emit response.created skeleton immediately.
	writeResponsesEvent(w, flusher, "response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"model":      opts.Model,
			"status":     "in_progress",
			"output":     []any{},
		},
	})

	// --- Accumulated state ---
	var (
		fullContent         strings.Builder
		fullThinkingContent strings.Builder
		contextUsagePct     float64
		usageCredits        *float64
		toolCallsFromStream []ToolCallInfo

		// Streaming lifecycle for the message output item.
		messageItemIndex = -1
		messageItemID    string

		// Streaming lifecycle for the current tool call.
		activeToolCallIndex  = -1
		activeToolCallID     string
		activeToolCallItemID string
		activeToolCallName   string
		activeToolCallCustom bool
		activeToolCallArgs   strings.Builder
		nextOutputIndex      int

		// Tool call deduplication and stable item identity across lifecycle
		// events and the final response.completed output.
		streamedToolCallIDs  = make(map[string]struct{})
		streamedToolCallSigs = make(map[string]struct{})
		toolCallItemIDs      = make(map[string]string)
	)

	// finishActiveToolCall closes the current tool call SSE lifecycle.
	finishActiveToolCall := func() {
		if activeToolCallIndex < 0 {
			return
		}
		args := activeToolCallArgs.String()
		if activeToolCallCustom {
			input := customToolInput(args)
			writeResponsesEvent(w, flusher, "response.custom_tool_call_input.delta", map[string]any{
				"type":         "response.custom_tool_call_input.delta",
				"output_index": activeToolCallIndex,
				"item_id":      activeToolCallItemID,
				"delta":        input,
			})
			writeResponsesEvent(w, flusher, "response.custom_tool_call_input.done", map[string]any{
				"type":         "response.custom_tool_call_input.done",
				"output_index": activeToolCallIndex,
				"item_id":      activeToolCallItemID,
				"input":        input,
			})
		} else {
			writeResponsesEvent(w, flusher, "response.function_call_arguments.done", map[string]any{
				"type":         "response.function_call_arguments.done",
				"output_index": activeToolCallIndex,
				"item_id":      activeToolCallItemID,
				"arguments":    args,
			})
		}
		writeResponsesEvent(w, flusher, "response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": activeToolCallIndex,
			"item": responsesToolCallItem(
				activeToolCallItemID,
				activeToolCallID,
				activeToolCallName,
				args,
				activeToolCallCustom,
			),
		})
		if activeToolCallID != "" {
			streamedToolCallIDs[activeToolCallID] = struct{}{}
		}
		streamedToolCallSigs[toolCallSignature(activeToolCallName, args)] = struct{}{}
		activeToolCallIndex = -1
		activeToolCallID = ""
		activeToolCallItemID = ""
		activeToolCallName = ""
		activeToolCallCustom = false
		activeToolCallArgs.Reset()
	}

	wasStreamed := func(tc ToolCallInfo) bool {
		if tc.ID != "" {
			if _, ok := streamedToolCallIDs[tc.ID]; ok {
				return true
			}
		}
		_, ok := streamedToolCallSigs[toolCallSignature(tc.Name, tc.Arguments)]
		return ok
	}

	for event := range events {
		switch event.Type {

		case EventTypeContent:
			if event.ContextUsagePercentage > 0 {
				contextUsagePct = event.ContextUsagePercentage
				continue
			}
			if event.Content == "" {
				continue
			}
			fullContent.WriteString(event.Content)

			// Open the message output item on first content chunk.
			if messageItemIndex < 0 {
				messageItemIndex = nextOutputIndex
				nextOutputIndex++
				messageItemID = generateOutputItemID("msg_")
				writeResponsesEvent(w, flusher, "response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"output_index": messageItemIndex,
					"item": map[string]any{
						"type":    "message",
						"id":      messageItemID,
						"role":    "assistant",
						"content": []any{},
						"status":  "in_progress",
					},
				})
				writeResponsesEvent(w, flusher, "response.content_part.added", map[string]any{
					"type":          "response.content_part.added",
					"item_index":    messageItemIndex,
					"content_index": 0,
					"part":          map[string]any{"type": "output_text", "text": ""},
				})
			}

			writeResponsesEvent(w, flusher, "response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_index":    messageItemIndex,
				"content_index": 0,
				"delta":         event.Content,
			})

		case EventTypeThinking:
			if event.ThinkingContent == "" {
				continue
			}
			fullThinkingContent.WriteString(event.ThinkingContent)
			// Thinking content is buffered; reasoning item is emitted post-stream.

		case EventTypeToolCallStart:
			if event.ToolCall == nil {
				continue
			}
			// Finish any prior tool call lifecycle. Kiro may repeat a start/stop
			// lifecycle for the same call ID; once streamed, ignore the duplicate
			// lifecycle so Codex does not execute a second call with empty input.
			finishActiveToolCall()
			if event.ToolCall.ID != "" {
				if _, alreadyStreamed := streamedToolCallIDs[event.ToolCall.ID]; alreadyStreamed {
					continue
				}
			}

			activeToolCallIndex = nextOutputIndex
			nextOutputIndex++
			activeToolCallID = event.ToolCall.ID
			activeToolCallName = event.ToolCall.Name
			activeToolCallCustom = isResponsesCustomTool(activeToolCallName, opts.CustomToolNames)
			activeToolCallItemID = responsesToolCallItemID(activeToolCallCustom)
			activeToolCallArgs.Reset()

			callID := activeToolCallID
			if callID == "" {
				callID = GenerateToolCallID()
				activeToolCallID = callID
			}
			toolCallItemIDs[callID] = activeToolCallItemID

			writeResponsesEvent(w, flusher, "response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": activeToolCallIndex,
				"item": responsesToolCallItem(
					activeToolCallItemID,
					callID,
					activeToolCallName,
					"",
					activeToolCallCustom,
				),
			})

		case EventTypeToolCallDelta:
			if event.ToolCall == nil || activeToolCallIndex < 0 {
				continue
			}
			activeToolCallArgs.WriteString(event.ToolCall.Arguments)
			if activeToolCallCustom {
				// Kiro streams the JSON wrapper in fragments. Buffer it and emit
				// the unwrapped free-form input when the call is complete.
				continue
			}
			for _, fragment := range splitToolArgumentDelta(event.ToolCall.Arguments) {
				writeResponsesEvent(w, flusher, "response.function_call_arguments.delta", map[string]any{
					"type":         "response.function_call_arguments.delta",
					"output_index": activeToolCallIndex,
					"item_id":      activeToolCallItemID,
					"delta":        fragment,
				})
			}

		case EventTypeToolCallStop:
			finishActiveToolCall()

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
			log.Error().Err(event.Error).Msg("Kiro API error during Responses API streaming — sending clean termination")
			// Close any open message item.
			if messageItemIndex >= 0 {
				writeResponsesEvent(w, flusher, "response.output_text.delta", map[string]any{
					"type":          "response.output_text.delta",
					"item_index":    messageItemIndex,
					"content_index": 0,
					"delta":         fmt.Sprintf("\n\n[Gateway error: %v]", event.Error),
				})
				writeResponsesEvent(w, flusher, "response.output_text.done", map[string]any{
					"type":          "response.output_text.done",
					"item_index":    messageItemIndex,
					"content_index": 0,
					"text":          fullContent.String() + fmt.Sprintf("\n\n[Gateway error: %v]", event.Error),
				})
				writeResponsesEvent(w, flusher, "response.output_item.done", map[string]any{
					"type":         "response.output_item.done",
					"output_index": messageItemIndex,
					"item": map[string]any{
						"type":    "message",
						"id":      messageItemID,
						"role":    "assistant",
						"status":  "completed",
						"content": buildOutputContentArray(fullContent.String() + fmt.Sprintf("\n\n[Gateway error: %v]", event.Error)),
					},
				})
			}
			// Emit response.completed so the client gets a clean termination.
			writeResponsesEvent(w, flusher, "response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":         responseID,
					"object":     "response",
					"created_at": createdAt,
					"model":      opts.Model,
					"status":     "completed",
					"output":     []any{},
					"usage": map[string]any{
						"input_tokens":  0,
						"output_tokens": 0,
						"total_tokens":  0,
					},
				},
			})
			return nil

		case EventTypeDone:
			// Handled post-loop.
		}
	}

	// --- Post-stream processing ---

	// Close any open tool call.
	finishActiveToolCall()

	// Parse bracket-style tool calls from accumulated content.
	bracketCalls := parser.ParseBracketToolCalls(fullContent.String())
	allToolCalls := mergeAndDeduplicateToolCalls(toolCallsFromStream, bracketCalls)

	// Emit fallback/bracket tool calls that weren't already streamed.
	for _, tc := range allToolCalls {
		if wasStreamed(tc) {
			continue
		}
		custom := isResponsesCustomTool(tc.Name, opts.CustomToolNames)
		itemID := responsesToolCallItemID(custom)
		callID := tc.ID
		if callID == "" {
			callID = GenerateToolCallID()
		}
		toolCallItemIDs[callID] = itemID
		idx := nextOutputIndex
		nextOutputIndex++

		writeResponsesEvent(w, flusher, "response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": idx,
			"item":         responsesToolCallItem(itemID, callID, tc.Name, "", custom),
		})
		if custom {
			input := customToolInput(tc.Arguments)
			writeResponsesEvent(w, flusher, "response.custom_tool_call_input.delta", map[string]any{
				"type":         "response.custom_tool_call_input.delta",
				"output_index": idx,
				"item_id":      itemID,
				"delta":        input,
			})
			writeResponsesEvent(w, flusher, "response.custom_tool_call_input.done", map[string]any{
				"type":         "response.custom_tool_call_input.done",
				"output_index": idx,
				"item_id":      itemID,
				"input":        input,
			})
		} else {
			writeResponsesEvent(w, flusher, "response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": idx,
				"item_id":      itemID,
				"delta":        tc.Arguments,
			})
			writeResponsesEvent(w, flusher, "response.function_call_arguments.done", map[string]any{
				"type":         "response.function_call_arguments.done",
				"output_index": idx,
				"item_id":      itemID,
				"arguments":    tc.Arguments,
			})
		}
		writeResponsesEvent(w, flusher, "response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": idx,
			"item":         responsesToolCallItem(itemID, callID, tc.Name, tc.Arguments, custom),
		})
	}

	// Close the message output item if it was opened.
	if messageItemIndex >= 0 {
		contentStr := fullContent.String()
		writeResponsesEvent(w, flusher, "response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_index":    messageItemIndex,
			"content_index": 0,
			"text":          contentStr,
		})
		writeResponsesEvent(w, flusher, "response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": messageItemIndex,
			"item": map[string]any{
				"type":    "message",
				"id":      messageItemID,
				"role":    "assistant",
				"status":  "completed",
				"content": buildOutputContentArray(contentStr),
			},
		})
	}

	// Calculate token usage.
	outputTokens := tokenizer.CountTokens(fullContent.String() + fullThinkingContent.String())
	inputTokens := opts.InputTokens
	if contextUsagePct > 0 && opts.MaxInputTokens > 0 {
		inputTokens = tokenizer.CalculatePromptTokens(outputTokens, contextUsagePct/100, opts.MaxInputTokens)
	}
	totalTokens := inputTokens + outputTokens

	// Build final output array for response.completed.
	finalOutput := buildFinalOutputItems(
		fullThinkingContent.String(),
		fullContent.String(),
		allToolCalls,
		messageItemID,
		opts.CustomToolNames,
		toolCallItemIDs,
	)

	usage := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
	}
	if usageCredits != nil {
		usage["credits_used"] = *usageCredits
	}

	// Emit response.completed with the full final response.
	writeResponsesEvent(w, flusher, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"model":      opts.Model,
			"status":     "completed",
			"output":     finalOutput,
			"usage":      usage,
		},
	})

	// Collect truncated tool calls for caller to save to truncation state.
	var truncated []ToolCallInfo
	for _, tc := range allToolCalls {
		if tc.IsTruncated {
			truncated = append(truncated, tc)
		}
	}
	return truncated
}

// ---------------------------------------------------------------------------
// BuildResponsesResponse — non-streaming response builder
// ---------------------------------------------------------------------------

// BuildResponsesResponse constructs a complete Responses API JSON response
// from a CollectedResponse.
func BuildResponsesResponse(resp *CollectedResponse, opts ResponsesNonStreamOptions) *models.ResponsesResponse {
	responseID := GenerateResponseID()
	createdAt := time.Now().Unix()

	// Build output items.
	var output []models.OutputItem

	// 1. Reasoning item (before text message, per spec).
	if resp.ThinkingContent != "" {
		rsID := generateOutputItemID("rs_")
		output = append(output, models.OutputItem{
			Type: "reasoning",
			ID:   rsID,
			Summary: []models.SummaryContent{
				{Type: "summary_text", Text: resp.ThinkingContent},
			},
		})
	}

	// 2. Text message item.
	if resp.Content != "" {
		msgID := generateOutputItemID("msg_")
		output = append(output, models.OutputItem{
			Type:   "message",
			ID:     msgID,
			Role:   "assistant",
			Status: "completed",
			Content: []models.OutputContent{
				{Type: "output_text", Text: resp.Content},
			},
		})
	}

	// 3. Tool call items.
	for _, tc := range resp.ToolCalls {
		custom := isResponsesCustomTool(tc.Name, opts.CustomToolNames)
		itemID := responsesToolCallItemID(custom)
		callID := tc.ID
		if callID == "" {
			callID = GenerateToolCallID()
		}
		if custom {
			output = append(output, models.OutputItem{
				Type:   "custom_tool_call",
				ID:     itemID,
				CallID: callID,
				Name:   tc.Name,
				Input:  customToolInput(tc.Arguments),
			})
		} else {
			output = append(output, models.OutputItem{
				Type:      "function_call",
				ID:        itemID,
				CallID:    callID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
	}

	// Calculate token usage.
	outputTokens := tokenizer.CountTokens(resp.Content + resp.ThinkingContent)
	inputTokens := opts.InputTokens
	if resp.ContextUsagePercentage > 0 && opts.MaxInputTokens > 0 {
		inputTokens = tokenizer.CalculatePromptTokens(outputTokens, resp.ContextUsagePercentage/100, opts.MaxInputTokens)
	}
	totalTokens := inputTokens + outputTokens

	usage := &models.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
	}

	return &models.ResponsesResponse{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Model:     opts.Model,
		Status:    "completed",
		Output:    output,
		Usage:     usage,
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// buildOutputContentArray constructs the content field for a completed message
// output item.
func buildOutputContentArray(text string) []map[string]any {
	if text == "" {
		return []map[string]any{}
	}
	return []map[string]any{
		{"type": "output_text", "text": text, "annotations": []any{}},
	}
}

// buildFinalOutputItems assembles the output array for the response.completed
// event, mirroring BuildResponsesResponse but using map[string]any for SSE.
func buildFinalOutputItems(
	thinkingContent string,
	textContent string,
	toolCalls []ToolCallInfo,
	messageItemID string,
	customToolNames map[string]bool,
	toolCallItemIDs map[string]string,
) []map[string]any {
	var items []map[string]any

	if thinkingContent != "" {
		items = append(items, map[string]any{
			"type": "reasoning",
			"id":   generateOutputItemID("rs_"),
			"summary": []map[string]any{
				{"type": "summary_text", "text": thinkingContent},
			},
		})
	}

	if textContent != "" {
		id := messageItemID
		if id == "" {
			id = generateOutputItemID("msg_")
		}
		items = append(items, map[string]any{
			"type":    "message",
			"id":      id,
			"role":    "assistant",
			"status":  "completed",
			"content": buildOutputContentArray(textContent),
		})
	}

	for _, tc := range toolCalls {
		custom := isResponsesCustomTool(tc.Name, customToolNames)
		callID := tc.ID
		if callID == "" {
			callID = GenerateToolCallID()
		}
		itemID := toolCallItemIDs[callID]
		if itemID == "" {
			itemID = responsesToolCallItemID(custom)
		}
		items = append(items, responsesToolCallItem(
			itemID,
			callID,
			tc.Name,
			tc.Arguments,
			custom,
		))
	}

	if items == nil {
		return []map[string]any{}
	}
	return items
}
