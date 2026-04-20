package middleware

import (
	"net/http"
	"strings"

	"github.com/jwadow/kiro-gateway/gateway/internal/errors"
)

// apiKeyRoute describes how to extract and validate the API key for a
// specific route, and which error format to use on failure.
type apiKeyRoute struct {
	// acceptXAPIKey indicates whether the x-api-key header is accepted
	// as an alternative to Authorization: Bearer.
	acceptXAPIKey bool
	// useAnthropicError selects Anthropic error format when true,
	// OpenAI error format when false.
	useAnthropicError bool
}

// protectedRoutes maps URL paths to their API key validation rules.
var protectedRoutes = map[string]apiKeyRoute{
	"/v1/models":           {acceptXAPIKey: false, useAnthropicError: false},
	"/v1/chat/completions": {acceptXAPIKey: false, useAnthropicError: false},
	"/v1/messages":         {acceptXAPIKey: true, useAnthropicError: true},
}

// Auth returns a chi-compatible middleware that validates the API key on
// protected endpoints. Unprotected paths (e.g. /, /health) pass through
// without any check.
//
// For OpenAI endpoints (/v1/models, /v1/chat/completions) the key must
// be provided via the Authorization: Bearer header.
//
// For the Anthropic endpoint (/v1/messages) the key may be provided via
// either the Authorization: Bearer header or the x-api-key header.
//
// On failure the middleware returns HTTP 401 with a format-appropriate
// JSON error body (OpenAI or Anthropic format).
func Auth(proxyAPIKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, protected := protectedRoutes[r.URL.Path]
			if !protected {
				next.ServeHTTP(w, r)
				return
			}

			// Extract the API key from the request.
			key := extractBearerToken(r)
			if key == "" && route.acceptXAPIKey {
				key = r.Header.Get("x-api-key")
			}

			if key != proxyAPIKey {
				writeAuthError(w, route.useAnthropicError)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractBearerToken extracts the token from an Authorization: Bearer header.
// Returns an empty string when the header is missing or malformed.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// writeAuthError writes a 401 Unauthorized response in the appropriate
// error format (OpenAI or Anthropic).
func writeAuthError(w http.ResponseWriter, anthropicFormat bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	if anthropicFormat {
		body := errors.AnthropicErrorResponse("Invalid API Key", "authentication_error")
		_, _ = w.Write(body)
	} else {
		body := errors.OpenAIErrorResponse("Invalid API Key", "authentication_error", "invalid_api_key")
		_, _ = w.Write(body)
	}
}
