// Package converter — OpenAI Responses API adapter.
//
// This file converts OpenAI Responses API requests into the unified internal
// types defined in core.go. The main entry point is ConvertResponsesRequest,
// which handles both string and array `input` fields, maps all supported input
// item types to UnifiedMessage values, and extracts tool definitions and the
// system prompt from `instructions`.
package converter

import (
	"encoding/json"
	"strings"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// ConvertResponsesResult
// ---------------------------------------------------------------------------

// ConvertResponsesResult holds the output of ConvertResponsesRequest.
type ConvertResponsesResult struct {
	Messages     []UnifiedMessage
	Tools        []UnifiedTool
	SystemPrompt string
}

// ---------------------------------------------------------------------------
// ConvertResponsesRequest — main entry point
// ---------------------------------------------------------------------------

// ConvertResponsesRequest converts an OpenAI Responses API request into
// unified messages, tools, and a system prompt. It handles:
//
//  1. String `input` — treated as a single user message.
//  2. Array `input` — each item is converted by type:
//     - "message"              → user or assistant UnifiedMessage
//     - "function_call"        → assistant message with ToolCalls
//     - "function_call_output" → accumulated and flushed as a user message
//                                with ToolResults (same pattern as openai.go)
//  3. `instructions` — used as the system prompt.
//  4. `tools` — converted to UnifiedTool values.
//
// Unknown input item types are skipped with a warning, maintaining forward
// compatibility as the Responses API evolves.
func ConvertResponsesRequest(req models.ResponsesRequest, _ *config.Config) (*ConvertResponsesResult, error) {
	systemPrompt := strings.TrimSpace(req.Instructions)
	messages := convertResponsesInput(req.Input)
	tools := convertResponsesTools(req.Tools)

	return &ConvertResponsesResult{
		Messages:     messages,
		Tools:        tools,
		SystemPrompt: systemPrompt,
	}, nil
}

// ---------------------------------------------------------------------------
// Input conversion
// ---------------------------------------------------------------------------

// convertResponsesInput converts the `input` field (string or []InputItem)
// into a slice of UnifiedMessage values.
func convertResponsesInput(input any) []UnifiedMessage {
	if input == nil {
		return nil
	}

	// String input — single user message.
	if s, ok := input.(string); ok {
		if s == "" {
			return nil
		}
		return []UnifiedMessage{{Role: "user", Content: s}}
	}

	// Array input — may arrive as []any from JSON unmarshalling.
	rawItems, ok := input.([]any)
	if !ok {
		// Try typed slice (e.g. from tests that build the struct directly).
		if typedItems, ok2 := input.([]models.InputItem); ok2 {
			return convertInputItemSlice(typedItems)
		}
		log.Warn().Str("type", typedName(input)).Msg("Responses API: unexpected input type, treating as empty")
		return nil
	}

	// Convert []any → []models.InputItem via JSON round-trip so we normalise
	// the raw unmarshalled map[string]any values into the typed struct.
	items := make([]models.InputItem, 0, len(rawItems))
	for _, raw := range rawItems {
		var item models.InputItem
		b, err := json.Marshal(raw)
		if err != nil {
			log.Warn().Err(err).Msg("Responses API: failed to re-marshal input item, skipping")
			continue
		}
		if err := json.Unmarshal(b, &item); err != nil {
			log.Warn().Err(err).Msg("Responses API: failed to unmarshal input item, skipping")
			continue
		}
		items = append(items, item)
	}
	return convertInputItemSlice(items)
}

// convertInputItemSlice converts a typed slice of InputItem values using the
// same pending-tool-results flush loop pattern as openai.go.
func convertInputItemSlice(items []models.InputItem) []UnifiedMessage {
	var (
		processed          []UnifiedMessage
		pendingToolResults []map[string]any
		pendingToolImages  []UnifiedImage
	)

	flushPending := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		um := UnifiedMessage{
			Role:        "user",
			Content:     "",
			ToolResults: pendingToolResults,
		}
		if len(pendingToolImages) > 0 {
			um.Images = pendingToolImages
		}
		processed = append(processed, um)
		pendingToolResults = nil
		pendingToolImages = nil
	}

	for _, item := range items {
		switch item.Type {
		case "function_call_output":
			// Tool result — accumulate until the next non-tool-result item.
			content := item.Output
			if content == "" {
				content = "(empty result)"
			}
			// Apply MAX_TOOL_RESULT_CONTENT_LENGTH truncation by reusing the
			// same extractToolResultContent helper (it handles empty → placeholder).
			tr := map[string]any{
				"type":        "tool_result",
				"tool_use_id": item.CallID,
				"content":     content,
			}
			pendingToolResults = append(pendingToolResults, tr)

		case "function_call":
			// Assistant tool call from history — flush pending tool results first.
			flushPending()
			tcMap := map[string]any{
				"id":   item.ID,
				"type": "function",
				"function": map[string]any{
					"name":      item.Name,
					"arguments": item.Arguments,
				},
			}
			processed = append(processed, UnifiedMessage{
				Role:      "assistant",
				Content:   "",
				ToolCalls: []map[string]any{tcMap},
			})

		case "message":
			// Conversational turn — flush any pending tool results first.
			flushPending()
			um := convertResponsesMessageItem(item)
			processed = append(processed, um)

		default:
			// Unknown item type — skip with a warning for forward compatibility.
			log.Warn().Str("type", item.Type).Msg("Responses API: unsupported input item type, skipping")
		}
	}

	// Flush any trailing tool results.
	flushPending()

	return processed
}

// convertResponsesMessageItem converts a single "message" InputItem to a
// UnifiedMessage.
func convertResponsesMessageItem(item models.InputItem) UnifiedMessage {
	role := item.Role
	if role == "" {
		role = "user"
	}

	um := UnifiedMessage{Role: role}

	if item.Content == nil {
		return um
	}

	// String content.
	if s, ok := item.Content.(string); ok {
		um.Content = s
		return um
	}

	// Array content — may be []any from JSON or []models.ContentBlock directly.
	var blocks []models.ContentBlock
	switch v := item.Content.(type) {
	case []any:
		for _, raw := range v {
			var cb models.ContentBlock
			b, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(b, &cb); err != nil {
				continue
			}
			blocks = append(blocks, cb)
		}
	case []models.ContentBlock:
		blocks = v
	}

	var textParts []string
	for _, cb := range blocks {
		switch cb.Type {
		case "input_text", "output_text":
			if cb.Text != "" {
				textParts = append(textParts, cb.Text)
			}
		case "input_image":
			if cb.ImageURL != nil && cb.ImageURL.URL != "" {
				img := extractOpenAIImageURL(map[string]any{
					"image_url": map[string]any{
						"url":    cb.ImageURL.URL,
						"detail": cb.ImageURL.Detail,
					},
				})
				if img != nil {
					um.Images = append(um.Images, *img)
				}
			}
		default:
			// Unknown content block type — skip.
			log.Warn().Str("type", cb.Type).Msg("Responses API: unsupported content block type, skipping")
		}
	}

	um.Content = strings.Join(textParts, "\n")
	return um
}

// ---------------------------------------------------------------------------
// Tool conversion
// ---------------------------------------------------------------------------

// convertResponsesTools converts Responses API tool definitions to the
// unified UnifiedTool format.
func convertResponsesTools(tools []models.ResponsesTool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]UnifiedTool, 0, len(tools))
	for _, t := range tools {
		if t.Type != "function" {
			log.Warn().Str("type", t.Type).Msg("Responses API: unsupported tool type, skipping")
			continue
		}
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{}
		}
		out = append(out, UnifiedTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// typedName returns a human-readable type name for unknown input values,
// used in warning messages.
func typedName(v any) string {
	if v == nil {
		return "nil"
	}
	switch v.(type) {
	case string:
		return "string"
	case []any:
		return "[]any"
	case map[string]any:
		return "map[string]any"
	default:
		return "unknown"
	}
}
