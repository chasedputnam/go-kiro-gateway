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
//     with ToolResults (same pattern as openai.go)
//  3. `instructions` — used as the system prompt.
//  4. `tools` — converted to UnifiedTool values.
//
// Unknown input item types are skipped with a warning, maintaining forward
// compatibility as the Responses API evolves.
func ConvertResponsesRequest(req models.ResponsesRequest, _ *config.Config) (*ConvertResponsesResult, error) {
	systemPrompt := strings.TrimSpace(req.Instructions)
	messages := convertResponsesInput(req.Input)
	tools := mergeResponsesTools(
		convertResponsesTools(req.Tools),
		convertResponsesAdditionalTools(req.Input),
	)

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

// normalizeResponsesToolItems collapses duplicate lifecycle representations
// of the same call. Codex may retain both response.output_item.* and the copy
// inside response.completed when their item IDs differ. Kiro requires exactly
// one tool use and one matching result per toolUseId.
func normalizeResponsesToolItems(items []models.InputItem) []models.InputItem {
	if len(items) == 0 {
		return nil
	}

	normalized := make([]models.InputItem, 0, len(items))
	callIndexes := make(map[string]int)
	outputIndexes := make(map[string]int)

	for _, item := range items {
		switch item.Type {
		case "function_call", "custom_tool_call":
			key := item.CallID
			if key == "" {
				key = item.ID
			}
			if key != "" {
				if index, exists := callIndexes[key]; exists {
					existing := normalized[index]
					if responsesToolCallPayloadEmpty(existing) && !responsesToolCallPayloadEmpty(item) {
						normalized[index] = item
					}
					continue
				}
				callIndexes[key] = len(normalized)
			}

		case "function_call_output", "custom_tool_call_output":
			if item.CallID != "" {
				if index, exists := outputIndexes[item.CallID]; exists {
					existing := normalized[index]
					existing.Output = mergeResponsesToolOutputs(existing.Output, item.Output)
					normalized[index] = existing
					continue
				}
				outputIndexes[item.CallID] = len(normalized)
			}
		}

		normalized = append(normalized, item)
	}
	return normalized
}

func responsesToolCallPayloadEmpty(item models.InputItem) bool {
	if item.Type == "custom_tool_call" {
		return item.Input == ""
	}
	return item.Arguments == ""
}

func mergeResponsesToolOutputs(left, right any) string {
	leftText := extractTextFromAny(left)
	rightText := extractTextFromAny(right)
	switch {
	case leftText == "":
		return rightText
	case rightText == "" || rightText == leftText:
		return leftText
	default:
		return leftText + "\n\n" + rightText
	}
}

// convertInputItemSlice converts a typed slice of InputItem values using the
// same pending-tool-results flush loop pattern as openai.go.
func convertInputItemSlice(items []models.InputItem) []UnifiedMessage {
	items = normalizeResponsesToolItems(items)

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
		case "function_call_output", "custom_tool_call_output":
			// Tool result — accumulate until the next non-tool-result item.
			content := extractTextFromAny(item.Output)
			if content == "" {
				content = "(empty result)"
			}
			tr := map[string]any{
				"type":        "tool_result",
				"tool_use_id": item.CallID,
				"content":     content,
			}
			pendingToolResults = append(pendingToolResults, tr)

		case "function_call", "custom_tool_call":
			// Assistant tool call from history — flush pending tool results first.
			flushPending()
			toolUseID := item.CallID
			if toolUseID == "" {
				toolUseID = item.ID
			}
			arguments := item.Arguments
			if item.Type == "custom_tool_call" {
				wrapped, _ := json.Marshal(map[string]any{"input": item.Input})
				arguments = string(wrapped)
			}
			tcMap := map[string]any{
				"id":   toolUseID,
				"type": "function",
				"function": map[string]any{
					"name":      item.Name,
					"arguments": arguments,
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

		case "additional_tools":
			// Metadata only. Tool definitions are extracted separately by
			// convertResponsesAdditionalTools and are not conversation messages.

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

// convertResponsesAdditionalTools extracts Codex's per-request tool inventory
// from input items of type "additional_tools".
func convertResponsesAdditionalTools(input any) []UnifiedTool {
	var definitions []models.ResponsesTool

	switch items := input.(type) {
	case []models.InputItem:
		for _, item := range items {
			if item.Type == "additional_tools" {
				definitions = append(definitions, item.Tools...)
			}
		}
	case []any:
		for _, raw := range items {
			b, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var item models.InputItem
			if err := json.Unmarshal(b, &item); err != nil {
				continue
			}
			if item.Type == "additional_tools" {
				definitions = append(definitions, item.Tools...)
			}
		}
	}

	return convertResponsesTools(definitions)
}

// convertResponsesTools converts Responses API tool definitions to the
// unified format. Namespace entries are containers and are recursively
// flattened because Kiro's tool protocol has no namespace wrapper.
func convertResponsesTools(tools []models.ResponsesTool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	var out []UnifiedTool
	var appendTool func(models.ResponsesTool)
	appendTool = func(t models.ResponsesTool) {
		switch t.Type {
		case "namespace":
			for _, child := range t.Tools {
				appendTool(child)
			}

		case "function":
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{}
			}
			out = append(out, UnifiedTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})

		case "custom":
			// Kiro only accepts JSON-schema tools. Wrap the free-form custom
			// input in one string property, then unwrap it again when emitting
			// Responses custom_tool_call events.
			out = append(out, UnifiedTool{
				Name:        t.Name,
				Description: t.Description,
				Kind:        "custom",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{
							"type":        "string",
							"description": "Raw free-form input for this custom tool.",
						},
					},
					"required":             []string{"input"},
					"additionalProperties": false,
				},
			})

		default:
			log.Warn().Str("type", t.Type).Msg("Responses API: unsupported tool type, skipping")
		}
	}

	for _, tool := range tools {
		appendTool(tool)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeResponsesTools combines top-level and additional tool inventories while
// retaining the first definition for each client-visible tool name.
func mergeResponsesTools(groups ...[]UnifiedTool) []UnifiedTool {
	seen := make(map[string]struct{})
	var out []UnifiedTool
	for _, group := range groups {
		for _, tool := range group {
			if tool.Name == "" {
				log.Warn().Msg("Responses API: tool with empty name, skipping")
				continue
			}
			if _, exists := seen[tool.Name]; exists {
				continue
			}
			seen[tool.Name] = struct{}{}
			out = append(out, tool)
		}
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
