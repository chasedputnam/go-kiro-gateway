package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/auth"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/cache"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/debug"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/resolver"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/server"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

// ---------------------------------------------------------------------------
// Mock AuthManager for tests
// ---------------------------------------------------------------------------

type mockAuthManager struct {
	accessToken string
	authTyp     auth.AuthType
	fingerprint string
	apiHost     string
	qHost       string
	profileARN  string
}

func (m *mockAuthManager) GetAccessToken(_ context.Context) (string, error) {
	return m.accessToken, nil
}

func (m *mockAuthManager) ForceRefresh(_ context.Context) error { return nil }
func (m *mockAuthManager) AuthType() auth.AuthType              { return m.authTyp }
func (m *mockAuthManager) ProfileARN() string                   { return m.profileARN }
func (m *mockAuthManager) Fingerprint() string                  { return m.fingerprint }
func (m *mockAuthManager) APIHost() string                      { return m.apiHost }
func (m *mockAuthManager) QHost() string                        { return m.qHost }

// Compile-time interface check.
var _ auth.AuthManager = (*mockAuthManager)(nil)

// ---------------------------------------------------------------------------
// Mock KiroClient for tests
// ---------------------------------------------------------------------------

type mockKiroClient struct{}

func (m *mockKiroClient) RequestWithRetry(_ context.Context, _, _ string, _ any, _ bool) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK}, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testConfig() *config.Config {
	return &config.Config{
		Host:                  "127.0.0.1",
		Port:                  0,
		ProxyAPIKey:           "test-key",
		Region:                "us-east-1",
		Version:               "test",
		Title:                 "Kiro Gateway",
		Description:           "Test",
		DebugMode:             "off",
		DebugDir:              "debug_logs",
		LogLevel:              "ERROR",
		ModelCacheTTL:         time.Hour,
		DefaultMaxInputTokens: 200000,
		HiddenModels:          map[string]string{},
		ModelAliases:          map[string]string{},
		HiddenFromList:        []string{},
		FallbackModels: []config.FallbackModel{
			{ModelID: "auto"},
			{ModelID: "claude-sonnet-4"},
		},
	}
}

func newMockAuth() *mockAuthManager {
	return &mockAuthManager{
		accessToken: "test-token",
		authTyp:     auth.AuthTypeKiroDesktop,
		apiHost:     "https://q.us-east-1.amazonaws.com",
		qHost:       "https://q.us-east-1.amazonaws.com",
	}
}

func testServer(cfg *config.Config) *server.Server {
	modelCache := cache.New(cfg.ModelCacheTTL)
	modelResolver := resolver.New(modelCache, resolver.Config{})
	debugLogger := debug.NewDebugLogger("off", "")
	truncState := truncation.NewState()

	return server.New(
		cfg,
		newMockAuth(),
		modelCache,
		modelResolver,
		&mockKiroClient{},
		debugLogger,
		truncState,
	)
}

// ---------------------------------------------------------------------------
// Tests: Graceful shutdown
// ---------------------------------------------------------------------------

func TestGracefulShutdown(t *testing.T) {
	cfg := testConfig()
	cfg.Port = 19876
	srv := testServer(cfg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Give the server a moment to start.
	time.Sleep(100 * time.Millisecond)

	// Verify the server is running.
	resp, err := http.Get("http://127.0.0.1:19876/health")
	if err != nil {
		t.Fatalf("server not reachable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Trigger graceful shutdown.
	if err := srv.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Server should have stopped.
	srvErr := <-errCh
	if srvErr != nil && srvErr != http.ErrServerClosed {
		t.Fatalf("unexpected server error: %v", srvErr)
	}
}

func TestShutdownTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.Port = 19877
	srv := testServer(cfg)

	go func() {
		_ = srv.Start()
	}()

	// Wait for the server to be ready by polling the health endpoint.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:19877/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Shutdown with a very short timeout should succeed when there
	// are no in-flight requests.
	if err := srv.Shutdown(100 * time.Millisecond); err != nil {
		t.Fatalf("shutdown with short timeout should succeed: %v", err)
	}
}

func TestShutdownBeforeStart(t *testing.T) {
	cfg := testConfig()
	srv := testServer(cfg)

	// Shutdown before Start should be a no-op.
	if err := srv.Shutdown(1 * time.Second); err != nil {
		t.Fatalf("shutdown before start should return nil: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: Startup model loading with fallback
// ---------------------------------------------------------------------------

func TestLoadModelsAtStartup_Fallback(t *testing.T) {
	cfg := testConfig()
	cfg.FallbackModels = []config.FallbackModel{
		{ModelID: "auto"},
		{ModelID: "claude-sonnet-4"},
		{ModelID: "claude-haiku-4.5"},
	}

	authMgr := &mockAuthManager{
		accessToken: "test-token",
		qHost:       "http://localhost:1", // unreachable
	}

	modelCache := cache.New(time.Hour)
	loadModelsAtStartup(cfg, authMgr, modelCache)

	ids := modelCache.GetAllModelIDs()
	if len(ids) != 3 {
		t.Fatalf("expected 3 fallback models, got %d: %v", len(ids), ids)
	}
	if !modelCache.IsValidModel("auto") {
		t.Error("expected 'auto' in cache")
	}
	if !modelCache.IsValidModel("claude-sonnet-4") {
		t.Error("expected 'claude-sonnet-4' in cache")
	}
}

func TestLoadModelsAtStartup_APISuccess(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ListAvailableModels" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{"modelId": "claude-sonnet-4.5", "maxInputTokens": 200000},
				{"modelId": "claude-haiku-4.5", "maxInputTokens": 200000}
			]
		}`))
	}))
	defer apiServer.Close()

	cfg := testConfig()
	authMgr := &mockAuthManager{
		accessToken: "test-token",
		qHost:       apiServer.URL,
	}

	modelCache := cache.New(time.Hour)
	loadModelsAtStartup(cfg, authMgr, modelCache)

	ids := modelCache.GetAllModelIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 API models, got %d: %v", len(ids), ids)
	}
	if !modelCache.IsValidModel("claude-sonnet-4.5") {
		t.Error("expected 'claude-sonnet-4.5' in cache")
	}
	if !modelCache.IsValidModel("claude-haiku-4.5") {
		t.Error("expected 'claude-haiku-4.5' in cache")
	}
}

func TestLoadModelsAtStartup_HiddenModels(t *testing.T) {
	cfg := testConfig()
	cfg.HiddenModels = map[string]string{
		"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	}

	authMgr := &mockAuthManager{
		accessToken: "test-token",
		qHost:       "http://localhost:1",
	}

	modelCache := cache.New(time.Hour)
	loadModelsAtStartup(cfg, authMgr, modelCache)

	for displayName, internalID := range cfg.HiddenModels {
		modelCache.AddHiddenModel(displayName, internalID)
	}

	if !modelCache.IsValidModel("claude-3.7-sonnet") {
		t.Error("expected hidden model 'claude-3.7-sonnet' in cache")
	}
}

// ---------------------------------------------------------------------------
// Tests: Version embedding
// ---------------------------------------------------------------------------

func TestVersionDefault(t *testing.T) {
	if version != "dev" {
		t.Errorf("expected default version 'dev', got %q", version)
	}
}

// ---------------------------------------------------------------------------
// Tests: fetchModelsFromKiro
// ---------------------------------------------------------------------------

func TestFetchModelsFromKiro_Success(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{"modelId": "model-a", "maxInputTokens": 100000},
				{"modelId": "model-b"}
			]
		}`))
	}))
	defer apiServer.Close()

	cfg := testConfig()
	authMgr := &mockAuthManager{
		accessToken: "test-token",
		qHost:       apiServer.URL,
	}

	result, err := fetchModelsFromKiro(context.Background(), cfg, authMgr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result))
	}
	if result[0].ModelID != "model-a" || result[0].MaxInputTokens != 100000 {
		t.Errorf("model-a: got %+v", result[0])
	}
	if result[1].MaxInputTokens != cfg.DefaultMaxInputTokens {
		t.Errorf("model-b maxInputTokens: expected %d, got %d", cfg.DefaultMaxInputTokens, result[1].MaxInputTokens)
	}
}

func TestFetchModelsFromKiro_APIError(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer apiServer.Close()

	cfg := testConfig()
	authMgr := &mockAuthManager{
		accessToken: "test-token",
		qHost:       apiServer.URL,
	}

	_, err := fetchModelsFromKiro(context.Background(), cfg, authMgr)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// ---------------------------------------------------------------------------
// Tests: printBanner (smoke test)
// ---------------------------------------------------------------------------

func TestPrintBanner(t *testing.T) {
	cfg := testConfig()
	cfg.Host = "0.0.0.0"
	cfg.Port = 8000
	printBanner(cfg)

	cfg.Host = "127.0.0.1"
	printBanner(cfg)
}

// ---------------------------------------------------------------------------
// Tests: Hidden models added to cache
// ---------------------------------------------------------------------------

func TestHiddenModelsAddedToCache(t *testing.T) {
	modelCache := cache.New(time.Hour)
	modelCache.Update([]models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "claude-sonnet-4"},
	})

	hiddenModels := map[string]string{
		"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	}
	for displayName, internalID := range hiddenModels {
		modelCache.AddHiddenModel(displayName, internalID)
	}

	if !modelCache.IsValidModel("claude-sonnet-4") {
		t.Error("expected 'claude-sonnet-4' in cache")
	}
	if !modelCache.IsValidModel("claude-3.7-sonnet") {
		t.Error("expected hidden model 'claude-3.7-sonnet' in cache")
	}
}
