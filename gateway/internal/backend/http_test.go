// Package backend — tests for HTTPBackend.
package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/streaming"
)

// ---------------------------------------------------------------------------
// Mock KiroClient
// ---------------------------------------------------------------------------

type mockKiroClient struct {
	resp *http.Response
	err  error
}

func (m *mockKiroClient) RequestWithRetry(_ context.Context, _, _ string, _ any, _ bool) (*http.Response, error) {
	return m.resp, m.err
}

// ---------------------------------------------------------------------------
// HTTPBackend tests
// ---------------------------------------------------------------------------

func TestHTTPBackend_Complete_ReturnsErrorOnClientFailure(t *testing.T) {
	b := NewHTTPBackend(&mockKiroClient{err: fmt.Errorf("connection refused")})
	_, err := b.Complete(context.Background(), &Request{KiroURL: "http://x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPBackend_Complete_ReturnsErrorOnNon200(t *testing.T) {
	body := io.NopCloser(bytes.NewBufferString(`{"error":"bad"}`))
	b := NewHTTPBackend(&mockKiroClient{
		resp: &http.Response{StatusCode: http.StatusBadGateway, Body: body},
	})
	_, err := b.Complete(context.Background(), &Request{KiroURL: "http://x"})
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}

func TestHTTPBackend_Complete_ChannelClosedOnEmptyStream(t *testing.T) {
	// An empty body will produce no events; channel should close cleanly.
	body := io.NopCloser(bytes.NewBufferString(""))
	b := NewHTTPBackend(&mockKiroClient{
		resp: &http.Response{StatusCode: http.StatusOK, Body: body},
	})

	opts := streaming.StreamOptions{}
	ch, err := b.Complete(context.Background(), &Request{
		KiroURL:    "http://x",
		StreamOpts: opts,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain the channel and verify it closes.
	for range ch {
	}
}

func TestHTTPBackend_Close_IsNoOp(t *testing.T) {
	b := NewHTTPBackend(&mockKiroClient{})
	if err := b.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}
