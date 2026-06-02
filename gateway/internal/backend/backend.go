// Package backend abstracts the downstream fulfillment mechanism for the
// gateway. Two implementations are provided:
//
//   - HTTPBackend: forwards requests to the Kiro HTTP API (existing behaviour)
//   - ACPBackend: communicates with a local kiro-cli process over JSON-RPC stdio
//
// Both implement the Backend interface, so route handlers are agnostic to
// which transport is in use.
package backend

import (
	"context"
	"fmt"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
)

// Backend abstracts the downstream fulfillment mechanism.
// The HTTP backend calls the Kiro API directly; the ACP backend
// communicates with a local kiro-cli process over JSON-RPC stdio.
type Backend interface {
	// Complete sends a request and returns a channel of KiroEvents.
	// The channel is closed when the response is complete or an error occurs.
	Complete(ctx context.Context, req *Request) (<-chan streaming.KiroEvent, error)

	// Close releases any resources held by the backend (e.g. subprocess).
	Close() error
}

// HTTPError is returned by HTTPBackend when the upstream API returns a
// non-200 status. Handlers can use errors.As to recover the code and body
// so they can forward the upstream status to the client.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("kiro API error %d: %s", e.StatusCode, e.Body)
}

// Request is the backend-agnostic representation of a completion request.
type Request struct {
	// Payload is the Kiro-format request payload produced by the converters.
	Payload map[string]any

	// Model is the resolved internal model ID.
	Model string

	// Stream indicates whether the client wants a streaming response.
	Stream bool

	// ProfileARN is the AWS profile ARN, required by the HTTP backend.
	ProfileARN string

	// ConversationID is the unique ID for this conversation turn.
	ConversationID string

	// KiroURL is the full URL for the HTTP backend endpoint.
	KiroURL string

	// MaxInputTokens is the model's maximum input token count (used for
	// token usage estimation in non-streaming responses).
	MaxInputTokens int

	// StreamOpts carries streaming configuration (thinking parser, timeouts).
	StreamOpts streaming.StreamOptions
}
