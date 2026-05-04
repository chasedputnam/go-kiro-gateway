package errors

import (
	"fmt"

	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// KiroErrorInfo
// ---------------------------------------------------------------------------

// KiroErrorInfo holds structured information about a Kiro API error,
// including the enhanced user-friendly message and the original error
// details for logging.
type KiroErrorInfo struct {
	// Reason is the error reason code from the Kiro API (e.g.
	// "CONTENT_LENGTH_EXCEEDS_THRESHOLD"). Defaults to "UNKNOWN" when
	// the API response does not include a reason field.
	Reason string

	// UserMessage is the enhanced, user-friendly message for end users.
	UserMessage string

	// OriginalMessage is the raw message from the Kiro API, preserved
	// for DEBUG-level logging.
	OriginalMessage string
}

// ---------------------------------------------------------------------------
// EnhanceKiroError
// ---------------------------------------------------------------------------

// EnhanceKiroError takes a parsed Kiro API error response (typically a
// map from JSON) and returns a KiroErrorInfo with a user-friendly message.
//
// Known reason codes are mapped to clear, actionable messages:
//   - CONTENT_LENGTH_EXCEEDS_THRESHOLD → context limit message
//   - MONTHLY_REQUEST_COUNT            → monthly quota message
//
// Unknown reasons preserve the original message and append the reason code.
// The original Kiro error is always logged at DEBUG level.
func EnhanceKiroError(errorJSON map[string]any) *KiroErrorInfo {
	originalMessage := stringFromMap(errorJSON, "message", "Unknown error")
	reason := stringFromMap(errorJSON, "reason", "UNKNOWN")

	// Log the original error at DEBUG level for troubleshooting.
	log.Debug().Str("reason", reason).Str("message", originalMessage).Msg("Kiro API error")

	var userMessage string

	switch reason {
	case "CONTENT_LENGTH_EXCEEDS_THRESHOLD":
		userMessage = "Model context limit reached. Conversation size exceeds model capacity."

	case "MONTHLY_REQUEST_COUNT":
		userMessage = "Monthly request limit exceeded. Account has reached its monthly quota."

	default:
		// Unknown error — include original message and reason code when
		// the reason is present and not the default "UNKNOWN".
		if reason != "UNKNOWN" {
			userMessage = fmt.Sprintf("%s (reason: %s)", originalMessage, reason)
		} else {
			userMessage = originalMessage
		}
	}

	return &KiroErrorInfo{
		Reason:          reason,
		UserMessage:     userMessage,
		OriginalMessage: originalMessage,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stringFromMap extracts a string value from a map, returning the fallback
// if the key is missing or the value is not a string.
func stringFromMap(m map[string]any, key, fallback string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}
