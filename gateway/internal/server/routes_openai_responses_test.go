package server_test

import (
	"bufio"
	"bytes"
	"encoding/json"
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
// Helpers specific to /v1/responses tests
// ---------------------------------------------------------------------------

// newTestServerWithResponsesMode creates a test server with the given
// OpenAIAPIMode set in the config.
func newTestServerWithResponsesMode(t *testing.T, mode string) *server.Server {
	t.Helper()

	cfg := &config.Config{
		Host:                     "127.0.0.1",
		Port:                     0,
		ProxyAPIKey:              "test-key",
		Version:                  "test",
		Title:                    "Kiro Gateway",
		Description:              "Test",
		DebugMode:                "off",
		TruncationRecovery:       false,
		FakeReasoningEnabled:     false,
		FakeReasoningMaxTokens:   8000,
		FakeReasoningHandling:    "as_reasoning_content",
		ToolDescriptionMaxLength: 10000,
		OpenAIAPIMode:            mode,
	}

	return newTestServerWithClientAndCfg(t, &mockStreamingClient{}, cfg)
}

// newTestServerWithClientAndCfg creates a test server using the provided
// config and mock streaming client.
func newTestServerWithClientAndCfg(t *testing.T, client *mockStreamingClient, cfg *config.Config) *server.Server {
	t.Helper()

	modelCache := cache.New(time.Hour)
	modelCache.Update([]models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4"},
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

// doResponsesRequest sends a POST /v1/responses request with the given body.
func doResponsesRequest(t *testing.T, srv *server.Server, body string, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	return rr
}

// parseResponsesSSEFromBody parses SSE events from the response body.
// Returns a slice of {eventType, data} pairs.
type testSSEEvent struct {
	EventType string
	Data      map[string]any
}

func parseResponsesSSEFromBody(body string) []testSSEEvent {
	var events []testSSEEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	var currentType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			var obj map[string]any
			if err := json.Unmarshal([]byte(dataStr), &obj); err == nil {
				events = append(events, testSSEEvent{EventType: currentType, Data: obj})
			}
			currentType = ""
		}
	}
	return events
}

// ---------------------------------------------------------------------------
// 11.1 Non-streaming and streaming success
// ---------------------------------------------------------------------------

func TestResponses_NonStreamingSuccess(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":"Hello!","stream":false}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["object"] != "response" {
		t.Errorf("object = %v, want response", resp["object"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status = %v, want completed", resp["status"])
	}
	id, _ := resp["id"].(string)
	if !strings.HasPrefix(id, "resp_") {
		t.Errorf("id = %q, want resp_ prefix", id)
	}
	if _, ok := resp["output"]; !ok {
		t.Error("expected output field in response")
	}
	if _, ok := resp["usage"]; !ok {
		t.Error("expected usage field in response")
	}
}

func TestResponses_StreamingSuccess(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":"Hello!","stream":true}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := parseResponsesSSEFromBody(rr.Body.String())
	if len(events) == 0 {
		t.Fatal("expected SSE events")
	}

	if events[0].EventType != "response.created" {
		t.Errorf("first event = %q, want response.created", events[0].EventType)
	}
	last := events[len(events)-1]
	if last.EventType != "response.completed" {
		t.Errorf("last event = %q, want response.completed", last.EventType)
	}
}

func TestResponses_ArrayInput_Success(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":[{"type":"message","role":"user","content":"hi"}],"stream":false}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 11.2 Validation error tests
// ---------------------------------------------------------------------------

func TestResponses_MissingModel_Returns422(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"input":"Hello!"}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	if errObj == nil {
		t.Fatal("expected error object")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "model") {
		t.Errorf("error message should mention 'model', got %q", msg)
	}
}

func TestResponses_MissingInput_Returns422(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4"}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "input") {
		t.Errorf("error message should mention 'input', got %q", msg)
	}
}

func TestResponses_EmptyStringInput_Returns422(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":""}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestResponses_EmptyArrayInput_Returns422(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":[]}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestResponses_InvalidJSON_Returns422(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `not valid json`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 11.3 Mode-flag and auth tests
// ---------------------------------------------------------------------------

func TestResponses_ChatMode_Returns501(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "chat")

	body := `{"model":"claude-sonnet-4","input":"Hello!"}`
	rr := doResponsesRequest(t, srv, body, "test-key")

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 in chat mode, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "OPENAI_API_MODE") {
		t.Errorf("error message should mention OPENAI_API_MODE, got %q", msg)
	}
}

func TestResponses_ChatMode_ChatCompletionsStillWorks(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	// Should not return 404 or 501; route is always registered.
	if rr.Code == http.StatusNotFound {
		t.Errorf("/v1/chat/completions returned 404 — route not registered")
	}
	if rr.Code == http.StatusNotImplemented {
		t.Errorf("/v1/chat/completions returned 501 in responses mode")
	}
}

func TestResponses_AuthFailure_Returns401(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":"Hello!"}`
	rr := doResponsesRequest(t, srv, body, "wrong-key")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestResponses_NoAuth_Returns401(t *testing.T) {
	srv := newTestServerWithResponsesMode(t, "responses")

	body := `{"model":"claude-sonnet-4","input":"Hello!"}`
	rr := doResponsesRequest(t, srv, body, "") // no API key

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
