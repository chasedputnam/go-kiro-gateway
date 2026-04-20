package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/middleware"
)

// dummyHandler is a simple handler that returns 200 OK with a body.
func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func TestCORS_PreflightRequest(t *testing.T) {
	handler := middleware.CORS()(dummyHandler())

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Preflight should return 200 or 204.
	if rr.Code != http.StatusOK && rr.Code != http.StatusNoContent {
		t.Errorf("expected 200 or 204 for preflight, got %d", rr.Code)
	}

	// Check CORS headers.
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected Access-Control-Allow-Origin=*, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods to be set")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Access-Control-Allow-Headers to be set")
	}
}

func TestCORS_RegularRequest(t *testing.T) {
	handler := middleware.CORS()(dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://example.com")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected Access-Control-Allow-Origin=*, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Auth middleware tests
// ---------------------------------------------------------------------------

const testAPIKey = "test-secret-key-123"

func TestAuth_ValidBearerToken(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid key, got %d", rr.Code)
	}
}

func TestAuth_MissingKey_Returns401(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	// No Authorization header.

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing key, got %d", rr.Code)
	}

	// Should be OpenAI error format.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in OpenAI error response")
	}
	if errObj["message"] != "Invalid API Key" {
		t.Errorf("expected 'Invalid API Key' message, got %v", errObj["message"])
	}
}

func TestAuth_InvalidKey_Returns401(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid key, got %d", rr.Code)
	}
}

func TestAuth_UnprotectedPath_NoAuthRequired(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	// Health endpoint should not require auth.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for unprotected path, got %d", rr.Code)
	}
}

func TestAuth_RootPath_NoAuthRequired(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for root path, got %d", rr.Code)
	}
}

func TestAuth_Anthropic_BearerToken(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid Bearer on /v1/messages, got %d", rr.Code)
	}
}

func TestAuth_Anthropic_XAPIKey(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", testAPIKey)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid x-api-key on /v1/messages, got %d", rr.Code)
	}
}

func TestAuth_Anthropic_InvalidKey_AnthropicErrorFormat(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "wrong-key")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	// Should be Anthropic error format.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if body["type"] != "error" {
		t.Errorf("expected Anthropic error format with type='error', got %v", body["type"])
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in Anthropic error response")
	}
	if errObj["type"] != "authentication_error" {
		t.Errorf("expected error type 'authentication_error', got %v", errObj["type"])
	}
	if errObj["message"] != "Invalid API Key" {
		t.Errorf("expected 'Invalid API Key' message, got %v", errObj["message"])
	}
}

func TestAuth_OpenAI_InvalidKey_OpenAIErrorFormat(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}

	// Should be OpenAI error format.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in OpenAI error response")
	}
	if errObj["type"] != "authentication_error" {
		t.Errorf("expected error type 'authentication_error', got %v", errObj["type"])
	}
	if errObj["code"] != "invalid_api_key" {
		t.Errorf("expected code 'invalid_api_key', got %v", errObj["code"])
	}
}

func TestAuth_Anthropic_MissingBothHeaders_Returns401(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// No Authorization or x-api-key header.

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing both headers on /v1/messages, got %d", rr.Code)
	}
}

func TestAuth_MalformedBearerToken(t *testing.T) {
	handler := middleware.Auth(testAPIKey)(dummyHandler())

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Basic "+testAPIKey) // Wrong scheme.

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for malformed Bearer token, got %d", rr.Code)
	}
}
