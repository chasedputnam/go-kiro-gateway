// Package client provides an HTTP client for the Kiro API with automatic retry
// logic, token refresh, and proxy support.
//
// The client handles transient failures transparently:
//   - 403: forces a token refresh via AuthManager and retries
//   - 429: exponential backoff (1s, 2s, 4s) and retry
//   - 5xx: exponential backoff and retry
//   - Timeouts: exponential backoff and retry
//
// For streaming requests, a per-request *http.Client is created with
// Transport.DisableKeepAlives = true to prevent CLOSE_WAIT connection leaks.
// Non-streaming requests share a pooled *http.Client for efficiency.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
)

// ---------------------------------------------------------------------------
// KiroClient interface
// ---------------------------------------------------------------------------

// KiroClient abstracts HTTP communication with the Kiro API. Callers use
// RequestWithRetry for all outbound requests; the implementation handles
// authentication headers, retries, and proxy routing.
type KiroClient interface {
	// RequestWithRetry sends an HTTP request with automatic retry logic.
	// For streaming requests (stream=true), the caller is responsible for
	// closing the returned *http.Response.Body.
	RequestWithRetry(ctx context.Context, method, url string, payload any, stream bool) (*http.Response, error)
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewKiroClient creates a KiroClient backed by a shared *http.Client for
// non-streaming requests. The shared client uses connection pooling via
// http.Transport for efficiency.
func NewKiroClient(authMgr auth.AuthManager, cfg *config.Config) KiroClient {
	transport := buildTransport(cfg)

	sharedClient := &http.Client{
		Transport: transport,
		Timeout:   300 * time.Second,
	}

	return &kiroHTTPClient{
		auth:         authMgr,
		sharedClient: sharedClient,
		config:       cfg,
	}
}

// ---------------------------------------------------------------------------
// kiroHTTPClient — concrete implementation
// ---------------------------------------------------------------------------

// kiroHTTPClient implements KiroClient. It holds a shared *http.Client for
// non-streaming requests and creates per-request clients for streaming.
type kiroHTTPClient struct {
	auth         auth.AuthManager
	sharedClient *http.Client
	config       *config.Config
}

// RequestWithRetry sends an HTTP request to the given URL with the specified
// method and JSON payload. It automatically retries on transient errors:
//
//   - 403: forces a token refresh and retries immediately
//   - 429: waits with exponential backoff (1s, 2s, 4s) then retries
//   - 5xx: waits with exponential backoff then retries
//   - Timeouts/network errors: waits with exponential backoff then retries
//
// For streaming requests the caller must close the response body. For
// non-streaming requests the response body is ready to read.
//
// When all retries are exhausted the returned error contains actionable
// troubleshooting information.
func (c *kiroHTTPClient) RequestWithRetry(ctx context.Context, method, reqURL string, payload any, stream bool) (*http.Response, error) {
	maxRetries := c.config.MaxRetries

	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Obtain a fresh access token for each attempt.
		token, err := c.auth.GetAccessToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get access token: %w", err)
		}

		// Marshal the payload to JSON.
		var bodyBytes []byte
		if payload != nil {
			bodyBytes, err = json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request payload: %w", err)
			}
		}

		// Build the HTTP request.
		req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP request: %w", err)
		}

		// Set headers.
		setKiroHeaders(req, token, c.auth.Fingerprint())
		if stream {
			req.Header.Set("Connection", "close")
		}

		// Pick the right client.
		client := c.clientForRequest(stream)

		log.Printf("Sending request to Kiro API (attempt %d/%d, stream=%v)...", attempt+1, maxRetries, stream)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err

			// Check if context was cancelled — no point retrying.
			if ctx.Err() != nil {
				return nil, fmt.Errorf("request cancelled: %w", ctx.Err())
			}

			// Network/timeout error — exponential backoff and retry.
			if attempt < maxRetries-1 {
				delay := c.backoffDelay(attempt)
				log.Printf("Request error: %v — waiting %v (attempt %d/%d)", err, delay, attempt+1, maxRetries)
				if err := sleepWithContext(ctx, delay); err != nil {
					return nil, fmt.Errorf("retry interrupted: %w", err)
				}
			}
			continue
		}

		// Successful HTTP round-trip — inspect status code.
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil

		case resp.StatusCode == http.StatusForbidden:
			// 403 — force token refresh and retry.
			drainAndClose(resp)
			log.Printf("Received 403, refreshing token (attempt %d/%d)", attempt+1, maxRetries)
			if refreshErr := c.auth.ForceRefresh(ctx); refreshErr != nil {
				log.Printf("Token refresh failed: %v", refreshErr)
			}
			lastErr = fmt.Errorf("HTTP 403 Forbidden")
			continue

		case resp.StatusCode == http.StatusTooManyRequests:
			// 429 — rate limited, exponential backoff.
			drainAndClose(resp)
			delay := c.backoffDelay(attempt)
			log.Printf("Received 429, waiting %v (attempt %d/%d)", delay, attempt+1, maxRetries)
			lastErr = fmt.Errorf("HTTP 429 Too Many Requests")
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, fmt.Errorf("retry interrupted: %w", err)
			}
			continue

		case resp.StatusCode >= 500 && resp.StatusCode < 600:
			// 5xx — server error, exponential backoff.
			drainAndClose(resp)
			delay := c.backoffDelay(attempt)
			log.Printf("Received %d, waiting %v (attempt %d/%d)", resp.StatusCode, delay, attempt+1, maxRetries)
			lastErr = fmt.Errorf("HTTP %d server error", resp.StatusCode)
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, fmt.Errorf("retry interrupted: %w", err)
			}
			continue

		default:
			// Other status codes (4xx except 403/429) — return as-is.
			return resp, nil
		}
	}

	// All retries exhausted — return an actionable error.
	return nil, buildExhaustedError(lastErr, maxRetries, stream)
}

// ---------------------------------------------------------------------------
// Client selection
// ---------------------------------------------------------------------------

// clientForRequest returns the appropriate *http.Client. For streaming
// requests a fresh client with DisableKeepAlives=true is created to prevent
// CLOSE_WAIT connection leaks. For non-streaming requests the shared pooled
// client is returned.
func (c *kiroHTTPClient) clientForRequest(stream bool) *http.Client {
	if !stream {
		return c.sharedClient
	}

	// Per-request client for streaming — disable keep-alives so the
	// connection is closed when the response body is drained.
	transport := buildTransport(c.config)
	transport.DisableKeepAlives = true

	return &http.Client{
		Transport: transport,
		// No overall timeout for streaming — the caller controls
		// cancellation via context.
	}
}

// ---------------------------------------------------------------------------
// Transport construction (proxy support)
// ---------------------------------------------------------------------------

// buildTransport creates an *http.Transport with connection pooling and
// optional proxy routing based on VPN_PROXY_URL configuration.
func buildTransport(cfg *config.Config) *http.Transport {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	if cfg.VPNProxyURL != "" {
		proxyURL := normalizeProxyURL(cfg.VPNProxyURL)
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			log.Printf("Warning: invalid VPN_PROXY_URL %q: %v — connecting directly", cfg.VPNProxyURL, err)
			return transport
		}

		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			// Exclude localhost from proxy routing.
			host := req.URL.Hostname()
			if isLocalhost(host) {
				return nil, nil // direct connection
			}
			return parsed, nil
		}

		log.Printf("Proxy configured: %s (localhost excluded)", proxyURL)
	}

	return transport
}

// normalizeProxyURL ensures the proxy URL has a scheme. If no scheme is
// present, it defaults to http://.
func normalizeProxyURL(rawURL string) string {
	if strings.Contains(rawURL, "://") {
		return rawURL
	}
	return "http://" + rawURL
}

// isLocalhost returns true if the host refers to the local machine.
func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// ---------------------------------------------------------------------------
// Headers
// ---------------------------------------------------------------------------

// setKiroHeaders sets the standard headers required by the Kiro API on the
// given request. This includes Authorization, Content-Type, User-Agent with
// machine fingerprint, and AWS-specific headers.
func setKiroHeaders(req *http.Request, token, fingerprint string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent",
		fmt.Sprintf("aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-%s", fingerprint))
	req.Header.Set("x-amz-user-agent",
		fmt.Sprintf("aws-sdk-js/1.0.27 KiroIDE-0.7.45-%s", fingerprint))
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("amz-sdk-invocation-id", generateUUID())
	req.Header.Set("amz-sdk-request", "attempt=1; max=3")
}

// ---------------------------------------------------------------------------
// Backoff and sleep helpers
// ---------------------------------------------------------------------------

// backoffDelay returns the exponential backoff duration for the given attempt.
// The base delay comes from config (default 1s), doubling each attempt:
// attempt 0 → 1s, attempt 1 → 2s, attempt 2 → 4s.
func (c *kiroHTTPClient) backoffDelay(attempt int) time.Duration {
	base := c.config.BaseRetryDelay
	shift := uint(attempt)
	return base * (1 << shift)
}

// sleepWithContext pauses for the given duration but returns early if the
// context is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// drainAndClose reads and discards the response body then closes it. This
// ensures the underlying TCP connection can be reused by the connection pool.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	// Read up to 4KB to allow connection reuse, then close.
	buf := make([]byte, 4096)
	for {
		_, err := resp.Body.Read(buf)
		if err != nil {
			break
		}
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Error construction
// ---------------------------------------------------------------------------

// buildExhaustedError creates an actionable error message when all retry
// attempts have been exhausted.
func buildExhaustedError(lastErr error, maxRetries int, stream bool) error {
	var msg string
	if stream {
		msg = fmt.Sprintf("Streaming request failed after %d attempts.", maxRetries)
	} else {
		msg = fmt.Sprintf("Request failed after %d attempts.", maxRetries)
	}

	if lastErr != nil {
		msg += fmt.Sprintf("\n\nLast error: %v", lastErr)
	}

	msg += "\n\nTroubleshooting:\n" +
		"1. Check your network connection and VPN/proxy settings\n" +
		"2. Verify your credentials are valid (try refreshing your token)\n" +
		"3. Check if the Kiro API is experiencing issues\n" +
		"4. If using a proxy, ensure VPN_PROXY_URL is configured correctly"

	return fmt.Errorf("%s", msg)
}

// ---------------------------------------------------------------------------
// UUID helper
// ---------------------------------------------------------------------------

// generateUUID returns a new random UUID v4 string. It uses the google/uuid
// package if available, otherwise falls back to a simple implementation.
func generateUUID() string {
	return uuid.NewString()
}
