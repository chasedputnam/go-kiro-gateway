// Package converter — OpenAI adapter.
//
// This file converts OpenAI Chat Completions requests into the unified
// internal types defined in core.go. The main entry point is
// ConvertOpenAIRequest, which extracts system messages, converts tool
// messages, handles images, and normalises tools from both the standard
// OpenAI format and the Cursor flat format.
package converter

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/models"
)

// ---------------------------------------------------------------------------
// ConvertOpenAIResult holds the output of ConvertOpenAIRequest.
// ---------------------------------------------------------------------------

// ConvertOpenAIResult bundles the unified messages, tools, and system prompt
// produced by converting an OpenAI ChatCompletionRequest.
type ConvertOpenAIResult struct {
	Messages     []UnifiedMessage
	Tools        []UnifiedTool
	SystemPrompt string
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest — main entry point
// ---------------------------------------------------------------------------

// ConvertOpenAIRequest converts an OpenAI ChatCompletionRequest into unified
// messages, tools, and a system prompt. It:
//
//  1. Extracts system messages and combines them into a single system prompt.
//  2. Converts tool messages into user messages with ToolResults.
//  3. Converts assistant tool_calls into the unified ToolCall format.
//  4. Extracts base64 images from image_url content blocks.
//  5. Converts tools from both standard OpenAI and Cursor flat formats.
//
// The caller can then pass the result to BuildKiroPayload.
func ConvertOpenAIRequest(req models.ChatCompletionRequest, cfg *config.Config) (*ConvertOpenAIResult, error) {
	systemPrompt, unifiedMessages := convertOpenAIMessages(req.Messages)
	unifiedTools := convertOpenAITools(req.Tools)

	return &ConvertOpenAIResult{
		Messages:     unifiedMessages,
		Tools:        unifiedTools,
		SystemPrompt: systemPrompt,
	}, nil
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

// convertOpenAIMessages splits system messages into a combined system prompt
// and converts the remaining messages into UnifiedMessage values.
//
// OpenAI "tool" role messages are accumulated and flushed as a single user
// message with ToolResults (matching the Python behaviour).
func convertOpenAIMessages(messages []models.ChatMessage) (string, []UnifiedMessage) {
	// 1. Extract system prompt.
	var systemParts []string
	var nonSystem []models.ChatMessage

	for _, msg := range messages {
		if msg.Role == "system" {
			systemParts = append(systemParts, extractTextFromAny(msg.Content))
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}
	systemPrompt := strings.TrimSpace(strings.Join(systemParts, "\n"))

	// 2. Process remaining messages, accumulating tool results.
	var (
		processed          []UnifiedMessage
		pendingToolResults []map[string]any
		pendingToolImages  []UnifiedImage
		totalToolCalls     int
		totalToolResults   int
		totalImages        int
	)

	for _, msg := range nonSystem {
		if msg.Role == "tool" {
			// Accumulate tool results until a non-tool message arrives.
			tr := map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     extractToolResultContent(msg.Content),
			}
			pendingToolResults = append(pendingToolResults, tr)
			totalToolResults++

			// Extract images from tool message content (e.g. MCP screenshots).
			toolImages := extractImagesFromContent(msg.Content)
			if len(toolImages) > 0 {
				pendingToolImages = append(pendingToolImages, toolImages...)
				totalImages += len(toolImages)
			}
			continue
		}

		// Flush any pending tool results before processing the next message.
		if len(pendingToolResults) > 0 {
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

		// Convert the regular message.
		um := UnifiedMessage{
			Role:    msg.Role,
			Content: extractTextFromAny(msg.Content),
		}

		switch msg.Role {
		case "assistant":
			tc := extractToolCallsFromOpenAI(msg.ToolCalls)
			if len(tc) > 0 {
				um.ToolCalls = tc
				totalToolCalls += len(tc)
			}

		case "user":
			// Check for embedded tool_result blocks in content.
			tr := extractToolResultsFromOpenAIContent(msg.Content)
			if len(tr) > 0 {
				um.ToolResults = tr
				totalToolResults += len(tr)
			}
			// Extract images from user message content.
			images := extractImagesFromContent(msg.Content)
			if len(images) > 0 {
				um.Images = images
				totalImages += len(images)
			}
		}

		processed = append(processed, um)
	}

	// Flush remaining tool results at the end.
	if len(pendingToolResults) > 0 {
		um := UnifiedMessage{
			Role:        "user",
			Content:     "",
			ToolResults: pendingToolResults,
		}
		if len(pendingToolImages) > 0 {
			um.Images = pendingToolImages
		}
		processed = append(processed, um)
	}

	if totalToolCalls > 0 || totalToolResults > 0 || totalImages > 0 {
		log.Printf("Converted %d OpenAI messages: %d tool_calls, %d tool_results, %d images",
			len(messages), totalToolCalls, totalToolResults, totalImages)
	}

	return systemPrompt, processed
}

// ---------------------------------------------------------------------------
// Tool call extraction
// ---------------------------------------------------------------------------

// extractToolCallsFromOpenAI converts the raw []any tool_calls from an
// OpenAI assistant message into the unified map format expected by
// BuildKiroPayload.
func extractToolCallsFromOpenAI(raw []any) []map[string]any {
	if len(raw) == 0 {
		return nil
	}

	var out []map[string]any
	for _, item := range raw {
		tc, ok := item.(map[string]any)
		if !ok {
			continue
		}

		funcRaw, _ := tc["function"].(map[string]any)
		if funcRaw == nil {
			continue
		}

		out = append(out, map[string]any{
			"id":   stringVal(tc, "id"),
			"type": "function",
			"function": map[string]any{
				"name":      stringVal(funcRaw, "name"),
				"arguments": stringVal(funcRaw, "arguments"),
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Tool result extraction
// ---------------------------------------------------------------------------

// extractToolResultContent returns the text content for a tool result,
// defaulting to "(empty result)" when the content is blank.
func extractToolResultContent(content any) string {
	text := extractTextFromAny(content)
	if text == "" {
		return "(empty result)"
	}
	return text
}

// extractToolResultsFromOpenAIContent looks for embedded tool_result content
// blocks inside a user message's content (list format).
func extractToolResultsFromOpenAIContent(content any) []map[string]any {
	list, ok := content.([]any)
	if !ok {
		return nil
	}

	var results []map[string]any
	for _, item := range list {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringVal(block, "type") != "tool_result" {
			continue
		}
		contentText := extractTextFromAny(block["content"])
		if contentText == "" {
			contentText = "(empty result)"
		}
		results = append(results, map[string]any{
			"type":        "tool_result",
			"tool_use_id": stringVal(block, "tool_use_id"),
			"content":     contentText,
		})
	}
	return results
}

// ---------------------------------------------------------------------------
// Image extraction
// ---------------------------------------------------------------------------

// extractImagesFromContent extracts base64 images from content blocks.
//
// Supported formats:
//   - OpenAI image_url with data: URL → extracts media type and base64 data
//   - Anthropic image with base64 source → extracts media type and data
//
// URL-based images (http/https) are logged as a warning and skipped because
// the Kiro API does not support fetching remote images.
func extractImagesFromContent(content any) []UnifiedImage {
	list, ok := content.([]any)
	if !ok {
		return nil
	}

	var images []UnifiedImage
	for _, item := range list {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}

		blockType := stringVal(block, "type")

		switch blockType {
		case "image_url":
			img := extractOpenAIImageURL(block)
			if img != nil {
				images = append(images, *img)
			}

		case "image":
			img := extractAnthropicImage(block)
			if img != nil {
				images = append(images, *img)
			}
		}
	}
	return images
}

// extractOpenAIImageURL handles an OpenAI image_url content block.
// It supports data: URLs (base64 inline) and logs a warning for http URLs.
func extractOpenAIImageURL(block map[string]any) *UnifiedImage {
	imageURLObj, _ := block["image_url"].(map[string]any)
	if imageURLObj == nil {
		return nil
	}

	url := stringVal(imageURLObj, "url")
	if url == "" {
		return nil
	}

	if strings.HasPrefix(url, "data:") {
		// Parse data URL: data:image/jpeg;base64,/9j/4AAQ...
		parts := strings.SplitN(url, ",", 2)
		if len(parts) != 2 || parts[1] == "" {
			log.Printf("Failed to parse image data URL: missing data after comma")
			return nil
		}
		header := parts[0] // "data:image/jpeg;base64"
		data := parts[1]

		// Extract media type from header.
		mediaPart := strings.SplitN(header, ";", 2)[0] // "data:image/jpeg"
		mediaType := strings.TrimPrefix(mediaPart, "data:")
		if mediaType == "" {
			mediaType = "image/jpeg"
		}

		return &UnifiedImage{
			MediaType: mediaType,
			Data:      data,
		}
	}

	if strings.HasPrefix(url, "http") {
		log.Printf("URL-based images are not supported by Kiro API, skipping: %.80s...", url)
		return nil
	}

	return nil
}

// extractAnthropicImage handles an Anthropic-style image content block with
// a base64 source.
func extractAnthropicImage(block map[string]any) *UnifiedImage {
	source, _ := block["source"].(map[string]any)
	if source == nil {
		return nil
	}

	srcType := stringVal(source, "type")
	switch srcType {
	case "base64":
		mediaType := stringVal(source, "media_type")
		if mediaType == "" {
			mediaType = "image/jpeg"
		}
		data := stringVal(source, "data")
		if data == "" {
			return nil
		}
		return &UnifiedImage{
			MediaType: mediaType,
			Data:      data,
		}

	case "url":
		url := stringVal(source, "url")
		log.Printf("URL-based images are not supported by Kiro API, skipping: %.80s...", url)
		return nil
	}

	return nil
}

// ---------------------------------------------------------------------------
// Tool conversion
// ---------------------------------------------------------------------------

// convertOpenAITools converts OpenAI Tool definitions to the unified
// UnifiedTool format. It supports two layouts:
//
//  1. Standard OpenAI: Tool.Function is non-nil → use Function fields.
//  2. Cursor flat:     Tool.Function is nil, Tool.Name is set → use top-level fields.
//
// Tools whose Type is not "function" are silently skipped.
func convertOpenAITools(tools []models.Tool) []UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	var out []UnifiedTool
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}

		// Standard OpenAI format takes priority.
		if t.Function != nil {
			schema := t.Function.Parameters
			if schema == nil {
				schema = map[string]any{}
			}
			out = append(out, UnifiedTool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: schema,
			})
			continue
		}

		// Cursor flat format fallback.
		if t.Name != "" {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{}
			}
			out = append(out, UnifiedTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
			continue
		}

		log.Printf("Skipping invalid tool: no function or name field found")
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// BuildOpenAIKiroPayload — convenience wrapper
// ---------------------------------------------------------------------------

// BuildOpenAIKiroPayload is a convenience function that converts an OpenAI
// request and immediately builds the Kiro API payload. It mirrors the Python
// build_kiro_payload entry point in converters_openai.py.
func BuildOpenAIKiroPayload(
	req models.ChatCompletionRequest,
	conversationID string,
	profileARN string,
	modelID string,
	cfg *config.Config,
) (*KiroPayloadResult, error) {
	converted, err := ConvertOpenAIRequest(req, cfg)
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

// ---------------------------------------------------------------------------
// Helpers (unexported, used only by tests via white-box access)
// ---------------------------------------------------------------------------

// marshalJSON is a small helper that marshals v to a JSON string, returning
// "{}" on error. Used internally for tool call argument normalisation.
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
