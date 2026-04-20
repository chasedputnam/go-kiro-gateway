package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
)

// ---------------------------------------------------------------------------
// Mock AuthManager
// ---------------------------------------------------------------------------

// mockAuthManager implements auth.AuthManager for testing.
type mockAuthManager struct {
	token          string
	fingerprint    string
	forceRefreshFn func(ctx context.Context) error
	refreshCount   atomic.Int32
}

func (m *mockAuthManager) GetAccessToken(_ context.Context) (string, error) {
	return m.token, nil
}

func (m *mockAuthManager) ForceRefresh(ctx context.Context) error {
	m.refreshCount.Add(1)
	if m.forceRefreshFn != nil {
		return m.forceRefreshFn(ctx)
	}
	return nil
}

func (m *mockAuthManager) AuthType() auth.AuthType { return auth.AuthTypeKiroDesktop }
func (m *mockAuthManager) ProfileARN() string      { return "" }
func (m *mockAuthManager) Fingerprint() string     { return m.fingerprint }
func (m *mockAuthManager) APIHost() string         { return "https://q.us-east-1.amazonaws.com" }
func (m *mockAuthManager) QHost() string           { return "https://q.us-east-1.amazonaws.com" }

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testConfig() *config.Config {
	return &config.Config{
		MaxRetries:     3,
		BaseRetryDelay: 10 * time.Millisecond, // fast for tests
		VPNProxyURL:    "",
	}
}

func testAuthManager() *mockAuthManager {
	return &mockAuthManager{
		token:       "test-access-token",
		fingerprint: "test-fingerprint-abc123",
	}
}

// ---------------------------------------------------------------------------
// Tests: Successful requests
// ---------------------------------------------------------------------------

func TestRequestWithRetry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, map[string]string{"key": "value"}, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestRequestWithRetry_SetsCorrectHeaders(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	authMgr := testAuthManager()
	authMgr.token = "my-bearer-token"
	authMgr.fingerprint = "fp-12345"

	client := NewKiroClient(authMgr, testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Verify Authorization header.
	if got := capturedHeaders.Get("Authorization"); got != "Bearer my-bearer-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-bearer-token")
	}

	// Verify Content-Type.
	if got := capturedHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	// Verify User-Agent contains fingerprint.
	ua := capturedHeaders.Get("User-Agent")
	if !strings.Contains(ua, "fp-12345") {
		t.Errorf("User-Agent should contain fingerprint, got: %s", ua)
	}
	if !strings.Contains(ua, "KiroIDE") {
		t.Errorf("User-Agent should contain KiroIDE, got: %s", ua)
	}

	// Verify x-amz-user-agent contains fingerprint.
	xua := capturedHeaders.Get("x-amz-user-agent")
	if !strings.Contains(xua, "fp-12345") {
		t.Errorf("x-amz-user-agent should contain fingerprint, got: %s", xua)
	}

	// Verify AWS-specific headers.
	if got := capturedHeaders.Get("x-amzn-codewhisperer-optout"); got != "true" {
		t.Errorf("x-amzn-codewhisperer-optout = %q, want %q", got, "true")
	}
	if got := capturedHeaders.Get("x-amzn-kiro-agent-mode"); got != "vibe" {
		t.Errorf("x-amzn-kiro-agent-mode = %q, want %q", got, "vibe")
	}

	// Verify invocation ID is set (UUID format).
	invID := capturedHeaders.Get("amz-sdk-invocation-id")
	if invID == "" {
		t.Error("amz-sdk-invocation-id should be set")
	}
}

func TestRequestWithRetry_StreamingSetsConnectionClose(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if got := capturedHeaders.Get("Connection"); got != "close" {
		t.Errorf("Connection header for streaming = %q, want %q", got, "close")
	}
}

// ---------------------------------------------------------------------------
// Tests: Retry on 403 with token refresh
// ---------------------------------------------------------------------------

func TestRequestWithRetry_403TriggersTokenRefresh(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	authMgr := testAuthManager()
	client := NewKiroClient(authMgr, testConfig())

	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}

	// Verify ForceRefresh was called.
	if got := authMgr.refreshCount.Load(); got != 1 {
		t.Errorf("ForceRefresh called %d times, want 1", got)
	}
}

func TestRequestWithRetry_403ExhaustedRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	authMgr := testAuthManager()
	client := NewKiroClient(authMgr, testConfig())

	_, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	if !strings.Contains(err.Error(), "failed after") {
		t.Errorf("error should mention exhausted retries, got: %v", err)
	}

	// ForceRefresh should have been called for each 403.
	if got := authMgr.refreshCount.Load(); got != 3 {
		t.Errorf("ForceRefresh called %d times, want 3", got)
	}
}

// ---------------------------------------------------------------------------
// Tests: Retry on 429 with exponential backoff
// ---------------------------------------------------------------------------

func TestRequestWithRetry_429ExponentialBackoff(t *testing.T) {
	var requestCount atomic.Int32
	var timestamps []time.Time

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timestamps = append(timestamps, time.Now())
		count := requestCount.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseRetryDelay = 50 * time.Millisecond // measurable but fast

	client := NewKiroClient(testAuthManager(), cfg)
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Verify we made 3 requests (2 retries + 1 success).
	if got := requestCount.Load(); got != 3 {
		t.Errorf("expected 3 requests, got %d", got)
	}

	// Verify backoff delays are increasing.
	if len(timestamps) >= 3 {
		delay1 := timestamps[1].Sub(timestamps[0])
		delay2 := timestamps[2].Sub(timestamps[1])
		// Second delay should be roughly double the first (with some tolerance).
		if delay2 < delay1 {
			t.Errorf("expected increasing backoff: delay1=%v, delay2=%v", delay1, delay2)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Retry on 5xx with exponential backoff
// ---------------------------------------------------------------------------

func TestRequestWithRetry_5xxRetries(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if got := requestCount.Load(); got != 2 {
		t.Errorf("expected 2 requests, got %d", got)
	}
}

func TestRequestWithRetry_502Retries(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Tests: Non-retryable status codes returned as-is
// ---------------------------------------------------------------------------

func TestRequestWithRetry_400ReturnedAsIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected no error for 400, got: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRequestWithRetry_422ReturnedAsIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected no error for 422, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Tests: Context cancellation
// ---------------------------------------------------------------------------

func TestRequestWithRetry_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the backoff sleep is interrupted.
	cancel()

	client := NewKiroClient(testAuthManager(), testConfig())
	_, err := client.RequestWithRetry(ctx, http.MethodPost, server.URL, nil, false)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ---------------------------------------------------------------------------
// Tests: Payload marshaling
// ---------------------------------------------------------------------------

func TestRequestWithRetry_MarshalPayload(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload := map[string]any{
		"model":    "claude-sonnet-4",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, payload, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	var decoded map[string]any
	if err := json.Unmarshal(capturedBody, &decoded); err != nil {
		t.Fatalf("failed to decode captured body: %v", err)
	}
	if decoded["model"] != "claude-sonnet-4" {
		t.Errorf("expected model=claude-sonnet-4, got %v", decoded["model"])
	}
}

func TestRequestWithRetry_NilPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) != 0 {
			t.Errorf("expected empty body for nil payload, got %d bytes", len(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Tests: Exhausted retries error message
// ---------------------------------------------------------------------------

func TestRequestWithRetry_ExhaustedRetriesErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	_, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "failed after 3 attempts") {
		t.Errorf("error should mention attempt count, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "Troubleshooting") {
		t.Errorf("error should contain troubleshooting steps, got: %s", errMsg)
	}
}

func TestRequestWithRetry_StreamingExhaustedRetriesErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	_, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, true)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	if !strings.Contains(err.Error(), "Streaming request failed") {
		t.Errorf("streaming error should mention streaming, got: %s", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Tests: Proxy configuration
// ---------------------------------------------------------------------------

func TestNormalizeProxyURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.100:8080", "http://192.168.1.100:8080"},
		{"http://192.168.1.100:8080", "http://192.168.1.100:8080"},
		{"https://192.168.1.100:8080", "https://192.168.1.100:8080"},
		{"socks5://192.168.1.100:1080", "socks5://192.168.1.100:1080"},
		{"127.0.0.1:7890", "http://127.0.0.1:7890"},
		{"http://user:pass@proxy.com:8080", "http://user:pass@proxy.com:8080"},
	}

	for _, tt := range tests {
		got := normalizeProxyURL(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeProxyURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsLocalhost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.1", false},
		{"example.com", false},
		{"0.0.0.0", false},
	}

	for _, tt := range tests {
		got := isLocalhost(tt.host)
		if got != tt.expected {
			t.Errorf("isLocalhost(%q) = %v, want %v", tt.host, got, tt.expected)
		}
	}
}

func TestBuildTransport_NoProxy(t *testing.T) {
	cfg := testConfig()
	cfg.VPNProxyURL = ""

	transport := buildTransport(cfg)
	if transport.Proxy != nil {
		t.Error("expected no proxy function when VPN_PROXY_URL is empty")
	}
}

func TestBuildTransport_WithProxy(t *testing.T) {
	cfg := testConfig()
	cfg.VPNProxyURL = "http://proxy.example.com:8080"

	transport := buildTransport(cfg)
	if transport.Proxy == nil {
		t.Fatal("expected proxy function when VPN_PROXY_URL is set")
	}

	// Test that non-localhost requests go through proxy.
	req, _ := http.NewRequest("GET", "https://api.example.com/test", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if proxyURL == nil {
		t.Error("expected proxy URL for non-localhost request")
	}
	if proxyURL.Host != "proxy.example.com:8080" {
		t.Errorf("proxy host = %q, want %q", proxyURL.Host, "proxy.example.com:8080")
	}
}

func TestBuildTransport_ProxyExcludesLocalhost(t *testing.T) {
	cfg := testConfig()
	cfg.VPNProxyURL = "http://proxy.example.com:8080"

	transport := buildTransport(cfg)

	localhostURLs := []string{
		"http://localhost:8000/test",
		"http://127.0.0.1:8000/test",
		"http://[::1]:8000/test",
	}

	for _, rawURL := range localhostURLs {
		req, _ := http.NewRequest("GET", rawURL, nil)
		proxyURL, err := transport.Proxy(req)
		if err != nil {
			t.Fatalf("proxy function returned error for %s: %v", rawURL, err)
		}
		if proxyURL != nil {
			t.Errorf("expected nil proxy for localhost URL %s, got %v", rawURL, proxyURL)
		}
	}
}

func TestBuildTransport_ProxyWithoutScheme(t *testing.T) {
	cfg := testConfig()
	cfg.VPNProxyURL = "192.168.1.100:8080"

	transport := buildTransport(cfg)
	if transport.Proxy == nil {
		t.Fatal("expected proxy function for URL without scheme")
	}

	req, _ := http.NewRequest("GET", "https://api.example.com/test", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("expected proxy URL")
	}
	if proxyURL.Scheme != "http" {
		t.Errorf("expected http scheme default, got %q", proxyURL.Scheme)
	}
}

// ---------------------------------------------------------------------------
// Tests: Shared vs per-request client
// ---------------------------------------------------------------------------

func TestClientForRequest_NonStreamingUsesSharedClient(t *testing.T) {
	authMgr := testAuthManager()
	cfg := testConfig()
	kc := NewKiroClient(authMgr, cfg).(*kiroHTTPClient)

	client1 := kc.clientForRequest(false)
	client2 := kc.clientForRequest(false)

	// Both should be the same shared client instance.
	if client1 != client2 {
		t.Error("non-streaming requests should reuse the shared client")
	}
}

func TestClientForRequest_StreamingCreatesNewClient(t *testing.T) {
	authMgr := testAuthManager()
	cfg := testConfig()
	kc := NewKiroClient(authMgr, cfg).(*kiroHTTPClient)

	client1 := kc.clientForRequest(true)
	client2 := kc.clientForRequest(true)

	// Each streaming request should get a new client.
	if client1 == client2 {
		t.Error("streaming requests should create new clients")
	}

	// Streaming client should not be the shared client.
	if client1 == kc.sharedClient {
		t.Error("streaming client should not be the shared client")
	}
}

// ---------------------------------------------------------------------------
// Tests: Backoff delay calculation
// ---------------------------------------------------------------------------

func TestBackoffDelay(t *testing.T) {
	cfg := testConfig()
	cfg.BaseRetryDelay = 1 * time.Second
	kc := &kiroHTTPClient{config: cfg}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
	}

	for _, tt := range tests {
		got := kc.backoffDelay(tt.attempt)
		if got != tt.expected {
			t.Errorf("backoffDelay(%d) = %v, want %v", tt.attempt, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: sleepWithContext
// ---------------------------------------------------------------------------

func TestSleepWithContext_CompletesNormally(t *testing.T) {
	start := time.Now()
	err := sleepWithContext(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("sleep completed too quickly: %v", elapsed)
	}
}

func TestSleepWithContext_CancelledEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := sleepWithContext(ctx, 5*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if elapsed > 1*time.Second {
		t.Errorf("sleep should have been interrupted quickly, took: %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Tests: buildExhaustedError
// ---------------------------------------------------------------------------

func TestBuildExhaustedError_NonStreaming(t *testing.T) {
	err := buildExhaustedError(nil, 3, false)
	msg := err.Error()

	if !strings.Contains(msg, "Request failed after 3 attempts") {
		t.Errorf("expected non-streaming message, got: %s", msg)
	}
	if !strings.Contains(msg, "Troubleshooting") {
		t.Errorf("expected troubleshooting steps, got: %s", msg)
	}
}

func TestBuildExhaustedError_Streaming(t *testing.T) {
	err := buildExhaustedError(nil, 3, true)
	msg := err.Error()

	if !strings.Contains(msg, "Streaming request failed after 3 attempts") {
		t.Errorf("expected streaming message, got: %s", msg)
	}
}

func TestBuildExhaustedError_WithLastError(t *testing.T) {
	lastErr := io.EOF
	err := buildExhaustedError(lastErr, 3, false)
	msg := err.Error()

	if !strings.Contains(msg, "Last error: EOF") {
		t.Errorf("expected last error in message, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// Tests: Mixed retry scenarios
// ---------------------------------------------------------------------------

func TestRequestWithRetry_MixedErrors(t *testing.T) {
	// Simulate: 429 → 500 → 200
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		switch count {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := NewKiroClient(testAuthManager(), testConfig())
	resp, err := client.RequestWithRetry(context.Background(), http.MethodPost, server.URL, nil, false)
	if err != nil {
		t.Fatalf("expected success after mixed retries, got: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if got := requestCount.Load(); got != 3 {
		t.Errorf("expected 3 requests, got %d", got)
	}
}
