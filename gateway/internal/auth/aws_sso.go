// AWS SSO OIDC token refresh logic.
//
// This file implements the refresher interface for AWS SSO OIDC credentials
// (Enterprise Kiro IDE and kiro-cli). It POSTs to the AWS SSO OIDC token
// endpoint with the current refresh token, client ID, and client secret,
// then parses the response for new tokens.
//
// Endpoint: https://oidc.{region}.amazonaws.com/token
// Method:   POST
// Body:     {"grantType": "refresh_token", "clientId": "...", "clientSecret": "...", "refreshToken": "..."}
// Response: {"accessToken": "...", "refreshToken": "...", "expiresIn": 3600, "tokenType": "Bearer"}
//
// IMPORTANT: The AWS SSO OIDC CreateToken API uses JSON with camelCase
// parameter names, not form-urlencoded with snake_case. This matches the
// Python implementation in kiro/auth.py.
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
	"time"
)

// ---------------------------------------------------------------------------
// URL template
// ---------------------------------------------------------------------------

// awsSSOOIDCURLTemplate is the AWS SSO OIDC token endpoint. The region
// placeholder is filled at call time. The SSO region may differ from the
// Kiro API region (e.g., SSO in ap-southeast-1 while API is in us-east-1).
const awsSSOOIDCURLTemplate = "https://oidc.%s.amazonaws.com/token"

// ---------------------------------------------------------------------------
// Request / response payloads
// ---------------------------------------------------------------------------

// awsSSORefreshRequest is the JSON body sent to the AWS SSO OIDC token
// endpoint. Field names use camelCase to match the AWS CreateToken API.
type awsSSORefreshRequest struct {
	GrantType    string `json:"grantType"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}

// awsSSORefreshResponse is the JSON body returned by the AWS SSO OIDC token
// endpoint. Field names use camelCase as returned by the AWS API.
type awsSSORefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	TokenType    string `json:"tokenType"`
}

// awsSSOErrorResponse represents an error response from the AWS SSO OIDC
// endpoint, used for diagnostic logging.
type awsSSOErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ---------------------------------------------------------------------------
// awsSSORefresher
// ---------------------------------------------------------------------------

// awsSSORefresher implements the refresher interface for AWS SSO OIDC
// credentials (Enterprise Kiro IDE and kiro-cli).
type awsSSORefresher struct{}

// refresh performs an AWS SSO OIDC token refresh.
//
// Steps:
//  1. POST JSON to https://oidc.{ssoRegion}.amazonaws.com/token
//  2. Parse the JSON response for accessToken, refreshToken, expiresIn
//  3. Persist the refreshed tokens back to the source (JSON file or SQLite)
//
// The caller (doRefresh in auth.go) holds the mutex and updates the
// kiroAuthManager fields with the returned values.
func (r *awsSSORefresher) refresh(ctx context.Context, m *kiroAuthManager) (string, string, time.Time, error) {
	if m.refreshToken == "" {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: refresh token is not set")
	}
	if m.clientID == "" {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: client ID is not set (required for AWS SSO OIDC)")
	}
	if m.clientSecret == "" {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: client secret is not set (required for AWS SSO OIDC)")
	}

	// Use SSO region for the OIDC endpoint (may differ from API region).
	ssoRegion := m.ssoRegion
	if ssoRegion == "" {
		ssoRegion = m.cfg.Region
	}
	url := fmt.Sprintf(awsSSOOIDCURLTemplate, ssoRegion)

	// Build JSON request body with camelCase field names matching the
	// AWS SSO OIDC CreateToken API.
	reqBody := awsSSORefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RefreshToken: m.refreshToken,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: marshal request: %w", err)
	}

	log.Printf("AWS SSO OIDC refresh request: url=%s, sso_region=%s, api_region=%s, client_id=%s...",
		url, ssoRegion, m.cfg.Region, truncateForLog(m.clientID, 8))

	// Build HTTP request with context for timeout/cancellation.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use a dedicated client with a 30-second timeout (matching Python).
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read the full response body for error reporting.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse AWS error for more details.
		var awsErr awsSSOErrorResponse
		if jsonErr := json.Unmarshal(respBody, &awsErr); jsonErr == nil && awsErr.Error != "" {
			log.Printf("AWS SSO OIDC error details: error=%s, description=%s",
				awsErr.Error, awsErr.ErrorDescription)
		}
		return "", "", time.Time{}, fmt.Errorf(
			"aws sso oidc refresh: server returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response JSON.
	var refreshResp awsSSORefreshResponse
	if err := json.Unmarshal(respBody, &refreshResp); err != nil {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: parse response: %w", err)
	}

	if refreshResp.AccessToken == "" {
		return "", "", time.Time{}, fmt.Errorf("aws sso oidc refresh: response does not contain accessToken")
	}

	// Calculate expiration with a 60-second safety buffer (matching Python).
	expiresIn := refreshResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600 // default 1 hour
	}
	expiresAt := time.Now().UTC().Truncate(time.Second).Add(
		time.Duration(expiresIn-60) * time.Second,
	)

	log.Printf("Token refreshed via AWS SSO OIDC, expires: %s", expiresAt.Format(time.RFC3339))

	// Persist refreshed tokens back to the credential source.
	saveRefreshedTokens(m, refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt)

	return refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// truncateForLog returns the first n characters of s followed by "..." for
// safe logging of sensitive values. Returns the full string if shorter than n.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
