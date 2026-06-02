// Package backend — HTTP backend implementation.
//
// HTTPBackend wraps the existing client.KiroClient and exposes it through the
// Backend interface. It calls RequestWithRetry, pipes the response body through
// ParseKiroStream, and returns the resulting KiroEvent channel.
package backend

import (
	"context"
	"io"
	"net/http"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/client"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
)

// HTTPBackend fulfills requests by calling the Kiro HTTP API.
type HTTPBackend struct {
	kiroClient client.KiroClient
}

// NewHTTPBackend creates an HTTPBackend wrapping the given KiroClient.
func NewHTTPBackend(kiroClient client.KiroClient) *HTTPBackend {
	return &HTTPBackend{kiroClient: kiroClient}
}

// Complete sends the request payload to the Kiro HTTP API and returns a
// channel of KiroEvents. The channel is closed when the stream ends or an
// error occurs. An error event is sent on the channel for non-200 responses.
func (b *HTTPBackend) Complete(ctx context.Context, req *Request) (<-chan streaming.KiroEvent, error) {
	resp, err := b.kiroClient.RequestWithRetry(ctx, "POST", req.KiroURL, req.Payload, true)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	// ParseKiroStream owns resp.Body and will read from it until EOF.
	// We close the body in a goroutine once the event channel is drained.
	events := streaming.ParseKiroStream(ctx, resp.Body, req.StreamOpts)
	wrapped := make(chan streaming.KiroEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(wrapped)
		for event := range events {
			wrapped <- event
		}
	}()
	return wrapped, nil
}

// Close is a no-op for the HTTP backend — no persistent resources to release.
func (b *HTTPBackend) Close() error {
	return nil
}
