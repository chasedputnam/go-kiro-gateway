package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/debug"
	"github.com/jwadow/kiro-gateway/gateway/internal/models"
	"github.com/jwadow/kiro-gateway/gateway/internal/resolver"
	"github.com/jwadow/kiro-gateway/gateway/internal/server"
	"github.com/jwadow/kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// POST /v1/messages — validation tests
// ---------------------------------------------------------------------------

func TestMessages_MissingModel(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp["type"] != "error" {
		t.Errorf("expected type=error, got %v", errResp["type"])
	}
	errObj, _ := errResp["error"].(map[string]any)
	if errObj == nil {
		t.Fatal("expected error object in response")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "model") {
		t.Errorf("expected error about model, got %q", msg)
	}
}

func TestMessages_EmptyMessages(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": [], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMessages_MissingMaxTokens(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
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
	if !strings.Contains(msg, "max_tokens") {
		t.Errorf("expected error about max_tokens, got %q", msg)
	}
}

func TestMessages_InvalidJSON(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{not valid json}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — auth tests
// ---------------------------------------------------------------------------

func TestMessages_AuthWithXAPIKey(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with x-api-key, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMessages_AuthWithBearer(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMessages_NoAuth(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	// Verify Anthropic error format.
	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp["type"] != "error" {
		t.Errorf("expected type=error, got %v", errResp["type"])
	}
}

func TestMessages_InvalidAuth(t *testing.T) {
	srv := newTestServerWithClient(t, &mockStreamingClient{})

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "wrong-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — non-streaming tests
// ---------------------------------------------------------------------------

func TestMessages_NonStreaming_Success(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
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

	// Verify Anthropic response structure.
	if resp["type"] != "message" {
		t.Errorf("expected type=message, got %v", resp["type"])
	}

	id, _ := resp["id"].(string)
	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("expected id to start with msg_, got %q", id)
	}

	if resp["role"] != "assistant" {
		t.Errorf("expected role=assistant, got %v", resp["role"])
	}

	if resp["model"] != "claude-sonnet-4" {
		t.Errorf("expected model=claude-sonnet-4, got %v", resp["model"])
	}

	// Verify usage is present.
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage in response")
	}
}

func TestMessages_NonStreaming_KiroError(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusBadRequest,
		body:       `{"message": "Improperly formed request"}`,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify Anthropic error format.
	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp["type"] != "error" {
		t.Errorf("expected type=error, got %v", errResp["type"])
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — streaming tests
// ---------------------------------------------------------------------------

func TestMessages_Streaming_Success(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024, "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
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

	// Verify the response contains Anthropic SSE events.
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "event: message_start") {
		t.Error("expected response to contain 'event: message_start'")
	}
	if !strings.Contains(responseBody, "event: message_stop") {
		t.Error("expected response to contain 'event: message_stop'")
	}
}

func TestMessages_Streaming_KiroError(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusInternalServerError,
		body:       `{"message": "Internal error"}`,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024, "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — model resolution tests
// ---------------------------------------------------------------------------

func TestMessages_ModelResolution(t *testing.T) {
	client := &mockStreamingClient{
		statusCode: http.StatusOK,
		body:       "",
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — truncation recovery tests
// ---------------------------------------------------------------------------

func TestMessages_TruncationRecovery_ToolResult(t *testing.T) {
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
	truncState.SaveToolTruncation("toolu_abc", "read_file", map[string]any{
		"reason": "truncated",
	})

	srv := server.New(cfg, &mockAuthManager{}, modelCache, modelResolver, client, debugLogger, truncState)

	// Send a request with a tool_result referencing the truncated call.
	reqBody := map[string]any{
		"model":      "claude-sonnet-4",
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": []map[string]any{
				{"type": "tool_use", "id": "toolu_abc", "name": "read_file", "input": map[string]any{}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "toolu_abc", "content": "file contents"},
			}},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /v1/messages — client error tests
// ---------------------------------------------------------------------------

func TestMessages_ClientError(t *testing.T) {
	client := &mockStreamingClient{
		err: io.ErrUnexpectedEOF,
	}
	srv := newTestServerWithClient(t, client)

	body := `{"model": "claude-sonnet-4", "messages": [{"role": "user", "content": "hello"}], "max_tokens": 1024}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify Anthropic error format.
	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp["type"] != "error" {
		t.Errorf("expected type=error, got %v", errResp["type"])
	}
}
