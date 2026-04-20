// Kiro Desktop token refresh logic.
//
// This file implements the refresher interface for non-SSO credentials
// (Kiro Desktop Auth). It POSTs to the Kiro auth endpoint with the
// current refresh token and parses the response for new tokens.
//
// Endpoint: https://prod.{region}.auth.desktop.kiro.dev/refreshToken
// Method:   POST
// Body:     {"refreshToken": "...", "profileArn": "..."}
// Response: {"accessToken": "...", "refreshToken": "...", "expiresIn": 3600, "profileArn": "..."}
//
// After a successful refresh the new tokens are persisted back to the
// source file (JSON) or SQLite database so they survive gateway restarts.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ---------------------------------------------------------------------------
// URL template
// ---------------------------------------------------------------------------

// kiroDesktopRefreshURLTemplate is the Kiro Desktop Auth token refresh
// endpoint. The region placeholder is filled at call time.
const kiroDesktopRefreshURLTemplate = "https://prod.%s.auth.desktop.kiro.dev/refreshToken"

// ---------------------------------------------------------------------------
// Request / response payloads
// ---------------------------------------------------------------------------

// kiroDesktopRefreshRequest is the JSON body sent to the refresh endpoint.
type kiroDesktopRefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn,omitempty"`
}

// kiroDesktopRefreshResponse is the JSON body returned by the refresh endpoint.
type kiroDesktopRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	ProfileARN   string `json:"profileArn"`
}

// ---------------------------------------------------------------------------
// kiroDesktopRefresher
// ---------------------------------------------------------------------------

// kiroDesktopRefresher implements the refresher interface for Kiro Desktop
// (non-SSO) credentials.
type kiroDesktopRefresher struct{}

// refresh performs a Kiro Desktop token refresh.
//
// Steps:
//  1. POST to https://prod.{region}.auth.desktop.kiro.dev/refreshToken
//  2. Parse the JSON response for accessToken, refreshToken, expiresIn
//  3. Persist the refreshed tokens back to the source (JSON file or SQLite)
//
// The caller (doRefresh in auth.go) holds the mutex and updates the
// kiroAuthManager fields with the returned values.
func (r *kiroDesktopRefresher) refresh(ctx context.Context, m *kiroAuthManager) (string, string, time.Time, error) {
	if m.refreshToken == "" {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: refresh token is not set")
	}

	url := fmt.Sprintf(kiroDesktopRefreshURLTemplate, m.cfg.Region)

	// Build request body.
	reqBody := kiroDesktopRefreshRequest{
		RefreshToken: m.refreshToken,
		ProfileARN:   m.profileARN,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: marshal request: %w", err)
	}

	// Build HTTP request with context for timeout/cancellation.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("KiroIDE-0.7.45-%s", m.fingerprint))

	// Use a dedicated client with a 30-second timeout (matching Python).
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read the full response body for error reporting.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf(
			"kiro desktop refresh: server returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response JSON.
	var refreshResp kiroDesktopRefreshResponse
	if err := json.Unmarshal(respBody, &refreshResp); err != nil {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: parse response: %w", err)
	}

	if refreshResp.AccessToken == "" {
		return "", "", time.Time{}, fmt.Errorf("kiro desktop refresh: response does not contain accessToken")
	}

	// Calculate expiration with a 60-second safety buffer (matching Python).
	expiresIn := refreshResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600 // default 1 hour
	}
	expiresAt := time.Now().UTC().Truncate(time.Second).Add(
		time.Duration(expiresIn-60) * time.Second,
	)

	// Update profile ARN if the server returned one.
	if refreshResp.ProfileARN != "" {
		m.profileARN = refreshResp.ProfileARN
	}

	log.Printf("Token refreshed via Kiro Desktop Auth, expires: %s", expiresAt.Format(time.RFC3339))

	// Persist refreshed tokens back to the credential source.
	saveRefreshedTokens(m, refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt)

	return refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt, nil
}

// ---------------------------------------------------------------------------
// Token persistence helpers
// ---------------------------------------------------------------------------

// saveRefreshedTokens writes the new tokens back to the original credential
// source (JSON file or SQLite database) so they survive gateway restarts.
func saveRefreshedTokens(m *kiroAuthManager, accessToken, refreshToken string, expiresAt time.Time) {
	if m.sqliteDB != "" {
		saveTokensToSQLite(m, accessToken, refreshToken, expiresAt)
	} else if m.credsFile != "" {
		saveTokensToFile(m.credsFile, accessToken, refreshToken, expiresAt, m.profileARN)
	}
}

// saveTokensToFile updates an existing JSON credentials file with the new
// tokens while preserving all other fields.
func saveTokensToFile(path, accessToken, refreshToken string, expiresAt time.Time, profileARN string) {
	// Expand ~ in path.
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}

	// Read existing data to preserve other fields.
	existing := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &existing) // best-effort; start fresh on error
	}

	existing["accessToken"] = accessToken
	existing["refreshToken"] = refreshToken
	existing["expiresAt"] = expiresAt.Format(time.RFC3339)
	if profileARN != "" {
		existing["profileArn"] = profileARN
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to marshal credentials for saving: %v", err)
		return
	}

	if err := os.WriteFile(path, out, 0600); err != nil {
		log.Printf("Warning: failed to save credentials to %s: %v", path, err)
		return
	}

	log.Printf("Credentials saved to %s", path)
}

// saveTokensToSQLite writes refreshed tokens back to the kiro-cli SQLite
// database. Delegates to saveCredentialsToSQLite in sqlite.go.
func saveTokensToSQLite(m *kiroAuthManager, accessToken, refreshToken string, expiresAt time.Time) {
	if err := saveCredentialsToSQLite(m.sqliteDB, m.sqliteTokenKey, accessToken, refreshToken, expiresAt); err != nil {
		log.Printf("Warning: failed to save credentials to SQLite: %v", err)
	}
}
