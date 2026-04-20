package errors

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// OpenAI error format
// ---------------------------------------------------------------------------

// OpenAIErrorResponse builds an error response body in the OpenAI format:
//
//	{"error": {"message": "...", "type": "...", "code": ..., "param": null}}
//
// The returned []byte is ready to write to the HTTP response.
func OpenAIErrorResponse(message, errType string, code any) []byte {
	resp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    code,
			"param":   nil,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// OpenAIValidationError returns a 422-style validation error in OpenAI format.
func OpenAIValidationError(message string) []byte {
	return OpenAIErrorResponse(
		SanitizeErrorMessage(message),
		"invalid_request_error",
		"validation_error",
	)
}

// OpenAINetworkError returns a network/connectivity error in OpenAI format.
func OpenAINetworkError(info *NetworkErrorInfo) []byte {
	return OpenAIErrorResponse(
		info.FormatUserMessage(),
		"connectivity_error",
		string(info.Category),
	)
}

// OpenAIKiroError returns a Kiro API error in OpenAI format.
func OpenAIKiroError(info *KiroErrorInfo, statusCode int) []byte {
	return OpenAIErrorResponse(
		info.UserMessage,
		"api_error",
		statusCode,
	)
}

// ---------------------------------------------------------------------------
// Anthropic error format
// ---------------------------------------------------------------------------

// AnthropicErrorResponse builds an error response body in the Anthropic format:
//
//	{"type": "error", "error": {"type": "...", "message": "..."}}
//
// The returned []byte is ready to write to the HTTP response.
func AnthropicErrorResponse(message, errType string) []byte {
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// AnthropicValidationError returns a 422-style validation error in Anthropic format.
func AnthropicValidationError(message string) []byte {
	return AnthropicErrorResponse(
		SanitizeErrorMessage(message),
		"invalid_request_error",
	)
}

// AnthropicNetworkError returns a network/connectivity error in Anthropic format.
func AnthropicNetworkError(info *NetworkErrorInfo) []byte {
	return AnthropicErrorResponse(
		info.FormatUserMessage(),
		"connectivity_error",
	)
}

// AnthropicKiroError returns a Kiro API error in Anthropic format.
func AnthropicKiroError(info *KiroErrorInfo) []byte {
	return AnthropicErrorResponse(
		info.UserMessage,
		"api_error",
	)
}

// ---------------------------------------------------------------------------
// Error sanitization
// ---------------------------------------------------------------------------

// bytesPattern matches Go byte-slice representations like b'...' or
// []byte{...} that may leak into error messages.
var bytesPattern = regexp.MustCompile(`(?:b'[^']*'|\[\]byte\{[^}]*\})`)

// SanitizeErrorMessage removes internal details from an error message
// before returning it to clients. This includes:
//   - Go byte-slice representations ([]byte{...})
//   - Python bytes literals (b'...')
//   - Internal file paths
//   - Stack traces
func SanitizeErrorMessage(msg string) string {
	// Remove bytes objects.
	sanitized := bytesPattern.ReplaceAllString(msg, "[binary data]")

	// Remove internal file paths (e.g. /home/user/project/...).
	sanitized = removeInternalPaths(sanitized)

	return strings.TrimSpace(sanitized)
}

// removeInternalPaths strips absolute file paths from error messages to
// avoid leaking server directory structure.
func removeInternalPaths(msg string) string {
	// Match Unix-style absolute paths with Go file extensions.
	pathPattern := regexp.MustCompile(`(?:/[\w.-]+)+\.go(?::\d+)?`)
	sanitized := pathPattern.ReplaceAllString(msg, "[internal]")

	// Match Windows-style absolute paths.
	winPathPattern := regexp.MustCompile(`(?:[A-Z]:\\[\w\\.-]+)+\.go(?::\d+)?`)
	sanitized = winPathPattern.ReplaceAllString(sanitized, "[internal]")

	return sanitized
}

// ---------------------------------------------------------------------------
// Generic error helpers
// ---------------------------------------------------------------------------

// FormatValidationErrors converts a list of field validation issues into a
// single human-readable string. Each issue is a map with "field" and "message"
// keys (or similar structure from request validation).
func FormatValidationErrors(issues []map[string]string) string {
	if len(issues) == 0 {
		return "Request validation failed"
	}

	var parts []string
	for _, issue := range issues {
		field := issue["field"]
		message := issue["message"]
		if field != "" && message != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", field, message))
		} else if message != "" {
			parts = append(parts, message)
		}
	}

	if len(parts) == 0 {
		return "Request validation failed"
	}
	return strings.Join(parts, "; ")
}
