// Package converter — Anthropic adapter.
//
// This file converts Anthropic Messages API requests into the unified
// internal types defined in core.go. The main entry point is
// ConvertAnthropicRequest, which extracts the system prompt (from the
// separate system field), converts tool_use and tool_result content blocks,
// handles images (including images inside tool_result blocks), and
// normalises tools from the Anthropic input_schema format.
package converter

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
)

// ---------------------------------------------------------------------------
// ConvertAnthropicResult holds the output of ConvertAnthropicRequest.
// ---------------------------------------------------------------------------

// ConvertAnthropicResult bundles the unified messages, tools, and system
// prompt produced by converting an AnthropicMessagesRequest.
type ConvertAnthropicResult struct {
	Messages     []UnifiedMessage
	Tools        []UnifiedTool
	SystemPrompt string
}

// ---------------------------------------------------------------------------
// ConvertAnthropicRequest — main entry point
// ---------------------------------------------------------------------------

// ConvertAnthropicRequest converts an Anthropic MessagesRequest into unified
// messages, tools, and a system prompt. It:
//
//  1. Extracts the system prompt from the separate system field (string or
//     list of content blocks for prompt caching).
//  2. Converts each message to UnifiedMessage:
//     - User messages → extract text, images, tool_result blocks
//     - Assistant messages → extract text, tool_use blocks
//  3. Converts tools from Anthropic input_schema format to UnifiedTool.
//
// The caller can then pass the result to BuildKiroPayload.
func ConvertAnthropicRequest(req models.AnthropicMessagesRequest, cfg *config.Config) (*ConvertAnthropicResult, error) {
	systemPrompt := extractSystemPrompt(req.System)
	unifiedMessages := convertAnthropicMessages(req.Messages)
	unifiedTools := convertAnthropicTools(req.Tools)

	return &ConvertAnthropicResult{
		Messages:     unifiedMessages,
		Tools:        unifiedTools,
		SystemPrompt: systemPrompt,
	}, nil
}

// ---------------------------------------------------------------------------
// System prompt extraction
// ---------------------------------------------------------------------------

// extractSystemPrompt extracts the system prompt text from the Anthropic
// system field. The Anthropic API accepts system in two formats:
//
//  1. String: "You are helpful"
//  2. List of content blocks: [{"type":"text","text":"...","cache_control":{...}}]
//
// The second format is used for prompt caching with cache_control. We
// extract only the text, ignoring cache_control (not supported by Kiro).
func extractSystemPrompt(system any) string {
	if system == nil {
		return ""
	}

	// Case 1: plain string.
	if s, ok := system.(string); ok {
		return s
	}

	// Case 2: list of content blocks.
	if blocks, ok := system.([]any); ok {
		var parts []string
		for _, item := range blocks {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if stringVal(block, "type") == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	// Fallback: stringify whatever we got.
	return ""
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

// convertAnthropicMessages converts Anthropic messages to unified format.
//
// It handles:
//   - Text content (string or list of text blocks)
//   - Tool use blocks (assistant messages)
//   - Tool result blocks (user messages)
//   - Images in user messages (top-level and inside tool_result blocks)
func convertAnthropicMessages(messages []models.AnthropicMessage) []UnifiedMessage {
	var (
		unified        []UnifiedMessage
		totalToolCalls int
		totalToolRes   int
		totalImages    int
	)

	for _, msg := range messages {
		textContent := convertAnthropicContentToText(msg.Content)

		um := UnifiedMessage{
			Role:    msg.Role,
			Content: textContent,
		}

		switch msg.Role {
		case "assistant":
			tc := extractToolUsesFromAnthropicContent(msg.Content)
			if len(tc) > 0 {
				um.ToolCalls = tc
				totalToolCalls += len(tc)
			}

		case "user":
			tr := extractToolResultsFromAnthropicContent(msg.Content)
			if len(tr) > 0 {
				um.ToolResults = tr
				totalToolRes += len(tr)
			}

			// Extract images from user message content (top-level).
			images := extractImagesFromContent(msg.Content)

			// Also extract images from inside tool_result content blocks
			// (e.g. screenshots returned by browser MCP tools).
			toolResultImages := extractImagesFromToolResults(msg.Content)
			if len(toolResultImages) > 0 {
				images = append(images, toolResultImages...)
			}

			if len(images) > 0 {
				um.Images = images
				totalImages += len(images)
			}
		}

		unified = append(unified, um)
	}

	if totalToolCalls > 0 || totalToolRes > 0 || totalImages > 0 {
		log.Printf("Converted %d Anthropic messages: %d tool_calls, %d tool_results, %d images",
			len(messages), totalToolCalls, totalToolRes, totalImages)
	}

	return unified
}

// ---------------------------------------------------------------------------
// Content text extraction
// ---------------------------------------------------------------------------

// convertAnthropicContentToText extracts text content from Anthropic message
// content. Content can be:
//   - String: "Hello, world!"
//   - List of content blocks: [{"type":"text","text":"Hello"}]
//
// Non-text blocks (tool_use, tool_result, image) are ignored.
func convertAnthropicContentToText(content any) string {
	if content == nil {
		return ""
	}

	// Plain string.
	if s, ok := content.(string); ok {
		return s
	}

	// List of content blocks.
	if blocks, ok := content.([]any); ok {
		var parts []string
		for _, item := range blocks {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if stringVal(block, "type") == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	}

	return ""
}

// ---------------------------------------------------------------------------
// Tool use extraction (assistant messages)
// ---------------------------------------------------------------------------

// extractToolUsesFromAnthropicContent extracts tool_use blocks from an
// Anthropic assistant message's content. Each tool_use block is converted
// to the unified tool call format used by BuildKiroPayload.
func extractToolUsesFromAnthropicContent(content any) []map[string]any {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}

	var toolCalls []map[string]any
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringVal(block, "type") != "tool_use" {
			continue
		}

		toolID := stringVal(block, "id")
		toolName := stringVal(block, "name")
		if toolID == "" || toolName == "" {
			continue
		}

		toolInput := block["input"]
		if toolInput == nil {
			toolInput = map[string]any{}
		}

		// Normalise input to a JSON string if it's a map, matching the
		// unified format where arguments is a string.
		var arguments any
		switch inp := toolInput.(type) {
		case string:
			arguments = inp
		case map[string]any:
			arguments = inp
		default:
			b, err := json.Marshal(inp)
			if err != nil {
				arguments = map[string]any{}
			} else {
				// Try to keep it as a map if possible.
				var m map[string]any
				if json.Unmarshal(b, &m) == nil {
					arguments = m
				} else {
					arguments = string(b)
				}
			}
		}

		toolCalls = append(toolCalls, map[string]any{
			"id":   toolID,
			"type": "function",
			"function": map[string]any{
				"name":      toolName,
				"arguments": arguments,
			},
		})
	}

	return toolCalls
}

// ---------------------------------------------------------------------------
// Tool result extraction (user messages)
// ---------------------------------------------------------------------------

// extractToolResultsFromAnthropicContent extracts tool_result blocks from
// an Anthropic user message's content. Each tool_result is converted to the
// unified tool result format.
func extractToolResultsFromAnthropicContent(content any) []map[string]any {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}

	var results []map[string]any
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringVal(block, "type") != "tool_result" {
			continue
		}

		toolUseID := stringVal(block, "tool_use_id")
		if toolUseID == "" {
			continue
		}

		resultContent := block["content"]
		contentText := extractToolResultContentText(resultContent)
		if contentText == "" {
			contentText = "(empty result)"
		}

		results = append(results, map[string]any{
			"type":        "tool_result",
			"tool_use_id": toolUseID,
			"content":     contentText,
		})
	}

	return results
}

// extractToolResultContentText extracts text from a tool_result's content
// field. The content can be a string, a list of content blocks, or nil.
func extractToolResultContentText(content any) string {
	if content == nil {
		return ""
	}

	if s, ok := content.(string); ok {
		return s
	}

	// List of content blocks — extract text blocks only.
	if blocks, ok := content.([]any); ok {
		return extractTextFromAny(blocks)
	}

	return ""
}

// ---------------------------------------------------------------------------
// Image extraction from tool_result blocks
// ---------------------------------------------------------------------------

// extractImagesFromToolResults extracts images from inside tool_result
// content blocks. Tool results in Anthropic format can contain images
// (e.g. screenshots from browser MCP tools). This function looks inside
// each tool_result's content list for image blocks.
func extractImagesFromToolResults(content any) []UnifiedImage {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}

	var images []UnifiedImage
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringVal(block, "type") != "tool_result" {
			continue
		}

		resultContent, ok := block["content"].([]any)
		if !ok {
			continue
		}

		// Reuse the shared image extraction from openai.go.
		toolResultImages := extractImagesFromContent(resultContent)
		images = append(images, toolResultImages...)
	}

	if len(images) > 0 {
		log.Printf("Extracted %d image(s) from tool_result content", len(images))
	}

	return images
}

// ---------------------------------------------------------------------------
// Tool conversion
// ---------------------------------------------------------------------------

// convertAnthropicTools converts Anthropic tool definitions to the unified
// UnifiedTool format. Anthropic tools use input_schema instead of
// parameters.
func convertAnthropicTools(tools []models.AnthropicTool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	var out []UnifiedTool
	for _, t := range tools {
		schema := t.InputSchema
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
// BuildAnthropicKiroPayload — convenience wrapper
// ---------------------------------------------------------------------------

// BuildAnthropicKiroPayload is a convenience function that converts an
// Anthropic request and immediately builds the Kiro API payload. It mirrors
// the Python anthropic_to_kiro entry point in converters_anthropic.py.
func BuildAnthropicKiroPayload(
	req models.AnthropicMessagesRequest,
	conversationID string,
	profileARN string,
	modelID string,
	cfg *config.Config,
) (*KiroPayloadResult, error) {
	converted, err := ConvertAnthropicRequest(req, cfg)
	if err != nil {
		return nil, err
	}

	return BuildKiroPayload(BuildKiroPayloadOptions{
		Messages:       converted.Messages,
		SystemPrompt:   converted.SystemPrompt,
		ModelID:        modelID,
		Tools:          converted.Tools,
		ConversationID: conversationID,
		ProfileARN:     profileARN,
		InjectThinking: true,
		Cfg:            cfg,
	})
}
