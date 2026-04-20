package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/debug"
	"github.com/jwadow/kiro-gateway/gateway/internal/resolver"
	"github.com/jwadow/kiro-gateway/gateway/internal/server"
	"github.com/jwadow/kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// Mock dependencies
// ---------------------------------------------------------------------------

// mockAuthManager implements auth.AuthManager for testing.
type mockAuthManager struct{}

func (m *mockAuthManager) GetAccessToken(_ context.Context) (string, error) {
	return "mock-token", nil
}
func (m *mockAuthManager) ForceRefresh(_ context.Context) error { return nil }
func (m *mockAuthManager) AuthType() auth.AuthType              { return auth.AuthTypeKiroDesktop }
func (m *mockAuthManager) ProfileARN() string                   { return "" }
func (m *mockAuthManager) Fingerprint() string                  { return "mock-fingerprint" }
func (m *mockAuthManager) APIHost() string                      { return "https://q.us-east-1.amazonaws.com" }
func (m *mockAuthManager) QHost() string                        { return "https://q.us-east-1.amazonaws.com" }

// mockKiroClient implements client.KiroClient for testing.
type mockKiroClient struct{}

func (m *mockKiroClient) RequestWithRetry(_ context.Context, _, _ string, _ any, _ bool) (*http.Response, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Test helper
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T) *server.Server {
	t.Helper()

	cfg := &config.Config{
		Host:        "127.0.0.1",
		Port:        0,
		ProxyAPIKey: "test-key",
		Version:     "2.3",
		Title:       "Kiro Gateway",
		Description: "Test gateway description",
		DebugMode:   "off",
	}

	modelCache := cache.New(time.Hour)
	modelResolver := resolver.New(modelCache, resolver.Config{})
	debugLogger := debug.NewDebugLogger("off", "")
	truncState := truncation.NewState()

	return server.New(
		cfg,
		&mockAuthManager{},
		modelCache,
		modelResolver,
		&mockKiroClient{},
		debugLogger,
		truncState,
	)
}

// ---------------------------------------------------------------------------
// Health route tests
// ---------------------------------------------------------------------------

func TestRoot_ReturnsCorrectJSON(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
	if body["message"] != "Test gateway description" {
		t.Errorf("expected message='Test gateway description', got %q", body["message"])
	}
	if body["version"] != "2.3" {
		t.Errorf("expected version=2.3, got %q", body["version"])
	}

	// Verify Content-Type.
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %q", ct)
	}
}

func TestHealth_ReturnsCorrectJSON(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if body["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", body["status"])
	}
	if body["version"] != "2.3" {
		t.Errorf("expected version=2.3, got %v", body["version"])
	}

	// Verify timestamp is present and parseable.
	ts, ok := body["timestamp"].(string)
	if !ok || ts == "" {
		t.Fatal("expected non-empty timestamp string")
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("timestamp %q is not valid RFC3339: %v", ts, err)
	}
}

func TestRoot_NoAuthRequired(t *testing.T) {
	srv := newTestServer(t)

	// No Authorization header — should still succeed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 without auth, got %d", rr.Code)
	}
}

func TestHealth_NoAuthRequired(t *testing.T) {
	srv := newTestServer(t)

	// No Authorization header — should still succeed.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 without auth, got %d", rr.Code)
	}
}

func TestHealth_CORSHeaders(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS origin *, got %q", got)
	}
}
