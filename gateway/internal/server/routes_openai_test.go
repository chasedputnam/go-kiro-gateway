package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/cache"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/debug"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/resolver"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/server"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// Mock HTTP client that returns a canned response
// ---------------------------------------------------------------------------

// mockStreamingClient returns a configurable mock response for testing.
type mockStreamingClient struct {
	statusCode int
	body       string
	err        error
}

func (m *mockStreamingClient) RequestWithRetry(_ context.Context, _, _ string, _ any, _ bool) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &http.Response{
		StatusCode: m.statusCode,
		Body:       io.NopCloser(strings.NewReader(m.body)),
		Header:     make(http.Header),
	}, nil
}

// ---------------------------------------------------------------------------
// Test helper — creates a server with configurable mock client
// ---------------------------------------------------------------------------

func newTestServerWithClient(t *testing.T, client *mockStreamingClient) *server.Server {
	t.Helper()

	cfg := &config.Config{
		Host:                     "127.0.0.1",
		Port:                     0,
		ProxyAPIKey:              "test-key",
		Version:                  "2.3",
		Title:                    "Kiro Gateway",
		Description:              "Test gateway description",
		DebugMode:                "off",
		TruncationRecovery:       true,
		FakeReasoningEnabled:     false,
		FakeReasoningMaxTokens:   8000,
		FakeReasoningHandling:    "as_reasoning_content",
		ToolDescriptionMaxLength: 10000,
	}

	modelCache := cache.New(time.Hour)
	modelCache.Update([]models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4"},
		{ModelID: "claude-haiku-4.5", MaxInputTokens: 200000, DisplayName: "Claude Haiku 4.5"},
	})

	modelResolver := resolver.New(modelCache, resolver.Config{})
	debugLogger := debug.NewDebugLogger("off", "")
	truncState := truncation.NewState()

	return server.New(
		cfg,
		&mockAuthManager{},
		modelCache,
		modelResolver,
		client,
		debugLogger,
		truncState,
	)
}

// ---------------------------------------------------------------------------
// GET /v1/models tests
// ---------------------------------------------------------------------------

func TestListModels_ReturnsOpenAIFormat(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if body["object"] != "list" {
		t.Errorf("expected object=list, got %v", body["object"])
	}

	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("expected data to be an array, got %T", body["data"])
	}

	if len(data) < 2 {
		t.Errorf("expected at least 2 models, got %d", len(data))
	}

	// Verify each model has the expected fields.
	for _, item := range data {
		model, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("expected model to be a map, got %T", item)
		}
		if _, ok := model["id"].(string); !ok {
			t.Error("model missing 'id' field")
		}
		if model["object"] != "model" {
			t.Errorf("expected object=model, got %v", model["object"])
		}
		if _, ok := model["owned_by"].(string); !ok {
			t.Error("model missing 'owned_by' field")
		}
	}
}

func TestListModels_RequiresAuth(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	// No Authorization header.
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestListModels_InvalidAuth(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — validation tests
// ---------------------------------------------------------------------------

func TestChatCompletions_MissingModel(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	if errObj == nil {
		t.Fatal("expected error object in response")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "model") {
		t.Errorf("expected error about model, got %q", msg)
	}
}

func TestChatCompletions_EmptyMessages(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": []}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletions_RequiresAuth(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — non-streaming tests
// ---------------------------------------------------------------------------

func TestChatCompletions_NonStreaming_Success(t *testing.T) {
	// The mock client returns an empty body (simulating an empty Kiro stream).
	// The handler will parse it and build a response.
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "", // Empty stream — will produce empty content.
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify response structure.
	if resp["object"] != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %v", resp["object"])
	}

	id, _ := resp["id"].(string)
	if !strings.HasPrefix(id, "chatcmpl-") {
		t.Errorf("expected id to start with chatcmpl-, got %q", id)
	}

	if resp["model"] != "claude-sonnet-4" {
		t.Errorf("expected model=claude-sonnet-4, got %v", resp["model"])
	}

	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("expected non-empty choices array")
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		t.Fatal("expected choice to be a map")
	}

	msg, _ := choice["message"].(map[string]any)
	if msg == nil {
		t.Fatal("expected message in choice")
	}
	if msg["role"] != "assistant" {
		t.Errorf("expected role=assistant, got %v", msg["role"])
	}

	// Verify usage is present.
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage in response")
	}
}

func TestChatCompletions_NonStreaming_KiroError(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusBadRequest,
		body:       `{"message": "Improperly formed request"}`,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	if errObj == nil {
		t.Fatal("expected error object in response")
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — streaming tests
// ---------------------------------------------------------------------------

func TestChatCompletions_Streaming_Success(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "", // Empty stream.
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify Content-Type is text/event-stream.
	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected Content-Type=text/event-stream, got %q", ct)
	}

	// Verify the response ends with [DONE].
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "data: [DONE]") {
		t.Error("expected response to contain 'data: [DONE]'")
	}
}

func TestChatCompletions_Streaming_KiroError(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusInternalServerError,
		body:       `{"message": "Internal error"}`,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — model resolution tests
// ---------------------------------------------------------------------------

func TestChatCompletions_ModelResolution(t *testing.T) {
	// Use a model name that needs normalization.
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	// "claude-sonnet-4" is in the cache, should resolve.
	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — truncation recovery tests
// ---------------------------------------------------------------------------

func TestChatCompletions_TruncationRecovery_ToolResult(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}

	cfg := &config.Config{
		Host:                     "127.0.0.1",
		Port:                     0,
		ProxyAPIKey:              "test-key",
		Version:                  "2.3",
		Title:                    "Kiro Gateway",
		Description:              "Test gateway description",
		DebugMode:                "off",
		TruncationRecovery:       true,
		FakeReasoningEnabled:     false,
		FakeReasoningMaxTokens:   8000,
		FakeReasoningHandling:    "as_reasoning_content",
		ToolDescriptionMaxLength: 10000,
	}

	modelCache := cache.New(time.Hour)
	modelCache.Update([]models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000},
	})
	modelResolver := resolver.New(modelCache, resolver.Config{})
	debugLogger := debug.NewDebugLogger("off", "")
	truncState := truncation.NewState()

	// Save a tool truncation entry.
	truncState.SaveToolTruncation("call_123", "read_file", map[string]any{
		"reason": "truncated",
	})

	srv := server.New(cfg, &mockAuthManager{}, modelCache, modelResolver, client, debugLogger, truncState)

	// Send a request with a tool result referencing the truncated call.
	reqBody := map[string]any{
		"model": "claude-sonnet-4",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "Let me read that file.", "tool_calls": []map[string]any{
				{"id": "call_123", "type": "function", "function": map[string]any{"name": "read_file", "arguments": "{}"}},
			}},
			{"role": "tool", "tool_call_id": "call_123", "content": "file contents here"},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	// Should succeed (the truncation recovery modifies the message internally).
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — client error tests
// ---------------------------------------------------------------------------

func TestChatCompletions_ClientError(t *testing.T) {
	client := &mockStreamingClient{
		err: io.ErrUnexpectedEOF,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
}
