// SQLite credential loading and saving for kiro-cli.
//
// This file implements credential loading from a kiro-cli SQLite database
// and saving refreshed tokens back to it. The database contains an auth_kv
// table with key-value pairs where the value column holds JSON token data.
//
// Uses modernc.org/sqlite (pure Go, no CGO) via the standard database/sql
// driver interface.
//
// Token keys are searched in priority order:
//  1. kirocli:social:token   — Social login (Google, GitHub, Microsoft, etc.)
//  2. kirocli:odic:token     — AWS SSO OIDC (kiro-cli corporate)
//  3. codewhisperer:odic:token — Legacy AWS SSO OIDC
//
// The value column contains JSON with token data:
//
//	{"refreshToken": "...", "accessToken": "...", "expiresAt": "...",
//	 "profileArn": "...", "clientId": "...", "clientSecret": "..."}
//
// Note: The JSON field names use camelCase (matching the creds file format),
// but the Python implementation uses snake_case. We support both formats
// when reading to handle either case.
package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Pure-Go SQLite driver — no CGO required.
	_ "modernc.org/sqlite"

	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// SQLite token key priority
// ---------------------------------------------------------------------------

// sqliteTokenKeys defines the priority order for searching token keys in the
// auth_kv table. The first key found wins.
var sqliteTokenKeys = []string{
	"kirocli:social:token",     // Social login (Google, GitHub, Microsoft, etc.)
	"kirocli:odic:token",       // AWS SSO OIDC (kiro-cli corporate)
	"codewhisperer:odic:token", // Legacy AWS SSO OIDC
}

// sqliteRegistrationKeys defines the priority order for searching device
// registration keys (clientId/clientSecret for AWS SSO OIDC).
var sqliteRegistrationKeys = []string{
	"kirocli:odic:device-registration",
	"codewhisperer:odic:device-registration",
}

// ---------------------------------------------------------------------------
// SQLite JSON structures
// ---------------------------------------------------------------------------

// sqliteTokenData represents the JSON structure stored in the auth_kv value
// column for token entries. Supports both camelCase and snake_case field
// names to handle different kiro-cli versions.
type sqliteTokenData struct {
	// camelCase fields (creds file format)
	RefreshToken string   `json:"refreshToken,omitempty"`
	AccessToken  string   `json:"accessToken,omitempty"`
	ProfileARN   string   `json:"profileArn,omitempty"`
	ExpiresAt    string   `json:"expiresAt,omitempty"`
	Region       string   `json:"region,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`

	// snake_case fields (Python/Rust kiro-cli format)
	RefreshTokenSnake string `json:"refresh_token,omitempty"`
	AccessTokenSnake  string `json:"access_token,omitempty"`
	ProfileARNSnake   string `json:"profile_arn,omitempty"`
	ExpiresAtSnake    string `json:"expires_at,omitempty"`
}

// sqliteRegistrationData represents the JSON structure stored in the auth_kv
// value column for device registration entries.
type sqliteRegistrationData struct {
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Region       string `json:"region,omitempty"`

	// snake_case variants
	ClientIDSnake     string `json:"client_id,omitempty"`
	ClientSecretSnake string `json:"client_secret,omitempty"`
}

// ---------------------------------------------------------------------------
// Load credentials from SQLite
// ---------------------------------------------------------------------------

// loadCredentialsFromSQLite reads credentials from a kiro-cli SQLite database.
// It searches the auth_kv table for token keys in priority order and returns
// the parsed credential data along with the key that was used (for saving
// back later).
//
// Returns:
//   - creds: parsed credential data (nil if no token found)
//   - tokenKey: the auth_kv key that was loaded from
//   - err: any error encountered
func loadCredentialsFromSQLite(dbPath string) (*credsFileData, string, error) {
	// Expand ~ in path.
	if len(dbPath) > 0 && dbPath[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			dbPath = filepath.Join(home, dbPath[1:])
		}
	}

	// Check file exists before opening.
	if _, err := os.Stat(dbPath); err != nil {
		return nil, "", fmt.Errorf("sqlite db not found: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, "", fmt.Errorf("opening sqlite db: %w", err)
	}
	defer db.Close()

	// Search for token data in priority order.
	var tokenJSON string
	var tokenKey string
	for _, key := range sqliteTokenKeys {
		var val string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&val)
		if err == nil && val != "" {
			tokenJSON = val
			tokenKey = key
			log.Info().Str("key", key).Msg("Loaded credentials from SQLite")
			break
		}
	}

	if tokenJSON == "" {
		return nil, "", fmt.Errorf("no token found in SQLite auth_kv table (tried keys: %v)", sqliteTokenKeys)
	}

	// Parse the token JSON.
	var td sqliteTokenData
	if err := json.Unmarshal([]byte(tokenJSON), &td); err != nil {
		return nil, "", fmt.Errorf("parsing token JSON from SQLite: %w", err)
	}

	// Build credsFileData, preferring camelCase fields but falling back to
	// snake_case if the camelCase field is empty.
	creds := &credsFileData{
		RefreshToken: coalesce(td.RefreshToken, td.RefreshTokenSnake),
		AccessToken:  coalesce(td.AccessToken, td.AccessTokenSnake),
		ProfileARN:   coalesce(td.ProfileARN, td.ProfileARNSnake),
		ExpiresAt:    coalesce(td.ExpiresAt, td.ExpiresAtSnake),
		Region:       td.Region,
	}

	// Load device registration (clientId/clientSecret) for AWS SSO OIDC.
	for _, key := range sqliteRegistrationKeys {
		var val string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&val)
		if err == nil && val != "" {
			var reg sqliteRegistrationData
			if jsonErr := json.Unmarshal([]byte(val), &reg); jsonErr == nil {
				creds.ClientID = coalesce(reg.ClientID, reg.ClientIDSnake)
				creds.ClientSecret = coalesce(reg.ClientSecret, reg.ClientSecretSnake)
				if reg.Region != "" && creds.Region == "" {
					creds.Region = reg.Region
				}
				log.Info().Str("key", key).Msg("Loaded device registration from SQLite")
			}
			break
		}
	}

	return creds, tokenKey, nil
}

// ---------------------------------------------------------------------------
// Save credentials to SQLite
// ---------------------------------------------------------------------------

// saveCredentialsToSQLite writes refreshed tokens back to the kiro-cli SQLite
// database. It updates the auth_kv row for the given key with fresh token
// data. If the key is empty, it tries all known token keys as a fallback.
//
// Parameters:
//   - dbPath: path to the SQLite database file
//   - key: the auth_kv key to update (from loadCredentialsFromSQLite)
//   - accessToken: the new access token
//   - refreshToken: the new refresh token
//   - expiresAt: the token expiration time
func saveCredentialsToSQLite(dbPath, key string, accessToken, refreshToken string, expiresAt time.Time) error {
	// Expand ~ in path.
	if len(dbPath) > 0 && dbPath[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			dbPath = filepath.Join(home, dbPath[1:])
		}
	}

	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("sqlite db not found for writing: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("opening sqlite db for writing: %w", err)
	}
	defer db.Close()

	// Read existing value to preserve fields we don't update (scopes, region, etc.).
	tokenData := make(map[string]any)
	if key != "" {
		var existingJSON string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&existingJSON)
		if err == nil && existingJSON != "" {
			_ = json.Unmarshal([]byte(existingJSON), &tokenData) // best-effort
		}
	}

	// Update token fields. Write both camelCase and snake_case for
	// compatibility with different kiro-cli versions.
	tokenData["accessToken"] = accessToken
	tokenData["access_token"] = accessToken
	tokenData["refreshToken"] = refreshToken
	tokenData["refresh_token"] = refreshToken
	tokenData["expiresAt"] = expiresAt.Format(time.RFC3339)
	tokenData["expires_at"] = expiresAt.Format(time.RFC3339)

	updatedJSON, err := json.Marshal(tokenData)
	if err != nil {
		return fmt.Errorf("marshaling token data for SQLite: %w", err)
	}

	// Try the specific key first.
	if key != "" {
		result, err := db.Exec("UPDATE auth_kv SET value = ? WHERE key = ?", string(updatedJSON), key)
		if err != nil {
			return fmt.Errorf("updating SQLite key %s: %w", key, err)
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			log.Info().Str("key", key).Msg("Credentials saved to SQLite")
			return nil
		}
		log.Warn().Str("key", key).Msg("No rows updated for SQLite key, trying fallback")
	}

	// Fallback: try all known token keys.
	for _, fallbackKey := range sqliteTokenKeys {
		result, err := db.Exec("UPDATE auth_kv SET value = ? WHERE key = ?", string(updatedJSON), fallbackKey)
		if err != nil {
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			log.Info().Str("key", fallbackKey).Msg("Credentials saved to SQLite (fallback)")
			return nil
		}
	}

	return fmt.Errorf("failed to save credentials to SQLite: no matching keys found in auth_kv")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// coalesce returns the first non-empty string from its arguments.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
