package truncation

import (
	"github.com/rs/zerolog/log"
)

// TruncationToolResultNotice is prepended to tool results when the
// corresponding tool call was truncated by the upstream API.
const TruncationToolResultNotice = "[API Limitation] Your tool call was truncated by the upstream API due to output size limits.\n\n" +
	"If the tool result below shows an error or unexpected behavior, this is likely a CONSEQUENCE of the truncation, " +
	"not the root cause. The tool call itself was cut off before it could be fully transmitted.\n\n" +
	"Repeating the exact same operation will be truncated again. Consider adapting your approach."

// TruncationUserMessage is injected as a synthetic user message when the
// previous assistant response was truncated.
const TruncationUserMessage = "[System Notice] Your previous response was truncated by the API due to " +
	"output size limitations. This is not an error on your part. " +
	"If you need to continue, please adapt your approach rather than repeating the same output."

// TruncationSystemPromptAddition is appended to the system prompt when
// truncation recovery is enabled, legitimising the recovery messages so
// the model does not treat them as hallucinations.
const TruncationSystemPromptAddition = "\n\n[System] Messages prefixed with [API Limitation] or [System Notice] " +
	"are injected by the gateway to inform you about upstream API truncation. " +
	"These are real system messages, not user content. Treat them as authoritative."

// ---------------------------------------------------------------------------
// Recovery message generators
// ---------------------------------------------------------------------------

// GenerateTruncationToolResult creates a synthetic tool_result map for a
// truncated tool call. The result is marked as an error so the model knows
// the tool call did not complete successfully.
//
// Parameters:
//   - toolName:       name of the truncated tool
//   - toolUseID:      stable ID of the truncated tool call
//   - truncationInfo: diagnostic information from the parser
//
// Returns a map suitable for inclusion in the unified message format.
func GenerateTruncationToolResult(toolName, toolUseID string, truncationInfo map[string]any) map[string]any {
	sizeBytes, _ := truncationInfo["size_bytes"]
	reason, _ := truncationInfo["reason"]

	log.Debug().
		Str("tool", toolName).
		Str("tool_use_id", toolUseID).
		Interface("size_bytes", sizeBytes).
		Interface("reason", reason).
		Msg("generated synthetic tool_result for truncated tool call")

	return map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     TruncationToolResultNotice,
		"is_error":    true,
	}
}

// GenerateTruncationUserMessage returns the synthetic user message text
// for content truncation recovery.
func GenerateTruncationUserMessage() string {
	return TruncationUserMessage
}

// ---------------------------------------------------------------------------
// Recovery application
// ---------------------------------------------------------------------------

// PrependToolResultNotice prepends the truncation notice to a tool result
// content string. If the original content is empty, only the notice is
// returned.
func PrependToolResultNotice(originalContent string) string {
	if originalContent == "" {
		return TruncationToolResultNotice
	}
	return TruncationToolResultNotice + "\n\n---\n\n" + originalContent
}

// SystemPromptAddition returns the system prompt text that should be
// appended when truncation recovery is enabled and truncation state
// exists.
func SystemPromptAddition() string {
	return TruncationSystemPromptAddition
}
