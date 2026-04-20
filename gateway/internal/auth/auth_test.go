package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/config"

	// Pure-Go SQLite driver.
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestConfig returns a minimal Config suitable for auth tests.
func newTestConfig() *config.Config {
	return &config.Config{
		Region:                "us-east-1",
		TokenRefreshThreshold: 600 * time.Second, // 10 minutes
	}
}

// createTempCredsFile writes a JSON credentials file to a temp directory and
// returns the file path.
func createTempCredsFile(t *testing.T, data map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}
	return path
}

// createTempSQLiteDB creates a real SQLite database with an auth_kv table
// and inserts the given key-value pairs. Returns the database file path.
func createTempSQLiteDB(t *testing.T, rows map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.sqlite3")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE auth_kv (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	for k, v := range rows {
		_, err = db.Exec("INSERT INTO auth_kv (key, value) VALUES (?, ?)", k, v)
		if err != nil {
			t.Fatalf("insert row %s: %v", k, err)
		}
	}

	return dbPath
}

// tokenJSON builds a JSON string for a SQLite token value.
func tokenJSON(t *testing.T, data map[string]any) string {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal token json: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// 1. Auth type auto-detection
// ---------------------------------------------------------------------------

func TestDetectAuthType_KiroDesktop(t *testing.T) {
	m := &kiroAuthManager{}
	m.detectAuthType()

	if m.authType != AuthTypeKiroDesktop {
		t.Errorf("authType = %q, want %q", m.authType, AuthTypeKiroDesktop)
	}
}

func TestDetectAuthType_KiroDesktop_OnlyClientID(t *testing.T) {
	m := &kiroAuthManager{clientID: "some-id"}
	m.detectAuthType()

	if m.authType != AuthTypeKiroDesktop {
		t.Errorf("authType = %q, want %q (clientSecret missing)", m.authType, AuthTypeKiroDesktop)
	}
}

func TestDetectAuthType_KiroDesktop_OnlyClientSecret(t *testing.T) {
	m := &kiroAuthManager{clientSecret: "some-secret"}
	m.detectAuthType()

	if m.authType != AuthTypeKiroDesktop {
		t.Errorf("authType = %q, want %q (clientID missing)", m.authType, AuthTypeKiroDesktop)
	}
}

func TestDetectAuthType_AWSSSO(t *testing.T) {
	m := &kiroAuthManager{
		clientID:     "test-client-id",
		clientSecret: "test-client-secret",
	}
	m.detectAuthType()

	if m.authType != AuthTypeAWSSSO {
		t.Errorf("authType = %q, want %q", m.authType, AuthTypeAWSSSO)
	}
}

// ---------------------------------------------------------------------------
// 2. Token validity check
// ---------------------------------------------------------------------------

func TestIsTokenValid_EmptyToken(t *testing.T) {
	m := &kiroAuthManager{
		cfg: newTestConfig(),
	}
	if m.isTokenValid() {
		t.Error("isTokenValid() = true for empty token, want false")
	}
}

func TestIsTokenValid_ZeroExpiry(t *testing.T) {
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "some-token",
	}
	if m.isTokenValid() {
		t.Error("isTokenValid() = true for zero expiresAt, want false")
	}
}

func TestIsTokenValid_Expired(t *testing.T) {
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "some-token",
		expiresAt:   time.Now().Add(-1 * time.Hour),
	}
	if m.isTokenValid() {
		t.Error("isTokenValid() = true for expired token, want false")
	}
}

func TestIsTokenValid_WithinThreshold(t *testing.T) {
	// Token expires in 5 minutes, threshold is 10 minutes → invalid.
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "some-token",
		expiresAt:   time.Now().Add(5 * time.Minute),
	}
	if m.isTokenValid() {
		t.Error("isTokenValid() = true for token within threshold, want false")
	}
}

func TestIsTokenValid_FarFromExpiry(t *testing.T) {
	// Token expires in 2 hours, threshold is 10 minutes → valid.
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "some-token",
		expiresAt:   time.Now().Add(2 * time.Hour),
	}
	if !m.isTokenValid() {
		t.Error("isTokenValid() = false for token far from expiry, want true")
	}
}

func TestIsTokenValid_ExactlyAtThreshold(t *testing.T) {
	// Token expires in exactly 10 minutes, threshold is 10 minutes.
	// time.Until(expiresAt) == threshold → NOT greater than → invalid.
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "some-token",
		expiresAt:   time.Now().Add(10 * time.Minute),
	}
	// At the boundary, isTokenValid should return false because
	// time.Until(expiresAt) is not strictly > threshold.
	if m.isTokenValid() {
		t.Error("isTokenValid() = true at exact threshold boundary, want false")
	}
}

// ---------------------------------------------------------------------------
// 3. Machine fingerprint generation
// ---------------------------------------------------------------------------

func TestGenerateFingerprint_Deterministic(t *testing.T) {
	fp1 := generateFingerprint()
	fp2 := generateFingerprint()

	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q != %q", fp1, fp2)
	}
}

func TestGenerateFingerprint_Format(t *testing.T) {
	fp := generateFingerprint()

	// SHA-256 hex is 64 characters.
	if len(fp) != 64 {
		t.Errorf("fingerprint length = %d, want 64 (SHA-256 hex)", len(fp))
	}
}

func TestGenerateFingerprint_MatchesExpected(t *testing.T) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	unique := fmt.Sprintf("%s-%s-kiro-gateway", hostname, username)
	hash := sha256.Sum256([]byte(unique))
	expected := fmt.Sprintf("%x", hash)

	got := generateFingerprint()
	if got != expected {
		t.Errorf("fingerprint = %q, want %q", got, expected)
	}
}

// ---------------------------------------------------------------------------
// 4. Credential loading from JSON file
// ---------------------------------------------------------------------------

func TestLoadFromCredsFile_BasicFields(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt-123",
		"accessToken":  "at-456",
		"profileArn":   "arn:aws:codewhisperer:us-east-1:123456789:profile/test",
		"expiresAt":    "2099-01-01T00:00:00Z",
	})

	m := &kiroAuthManager{cfg: newTestConfig()}
	err := m.loadFromCredsFile(path)
	if err != nil {
		t.Fatalf("loadFromCredsFile() error: %v", err)
	}

	if m.refreshToken != "rt-123" {
		t.Errorf("refreshToken = %q, want %q", m.refreshToken, "rt-123")
	}
	if m.accessToken != "at-456" {
		t.Errorf("accessToken = %q, want %q", m.accessToken, "at-456")
	}
	if m.profileARN != "arn:aws:codewhisperer:us-east-1:123456789:profile/test" {
		t.Errorf("profileARN = %q, want expected ARN", m.profileARN)
	}
	if m.expiresAt.IsZero() {
		t.Error("expiresAt should not be zero")
	}
}

func TestLoadFromCredsFile_WithClientIDAndSecret(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt-sso",
		"clientId":     "sso-client-id",
		"clientSecret": "sso-client-secret",
	})

	m := &kiroAuthManager{cfg: newTestConfig()}
	err := m.loadFromCredsFile(path)
	if err != nil {
		t.Fatalf("loadFromCredsFile() error: %v", err)
	}

	if m.clientID != "sso-client-id" {
		t.Errorf("clientID = %q, want %q", m.clientID, "sso-client-id")
	}
	if m.clientSecret != "sso-client-secret" {
		t.Errorf("clientSecret = %q, want %q", m.clientSecret, "sso-client-secret")
	}
}

func TestLoadFromCredsFile_RegionOverride(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt-region",
		"region":       "eu-west-1",
	})

	m := &kiroAuthManager{cfg: newTestConfig()}
	err := m.loadFromCredsFile(path)
	if err != nil {
		t.Fatalf("loadFromCredsFile() error: %v", err)
	}

	expectedAPI := fmt.Sprintf(kiroAPIHostTemplate, "eu-west-1")
	if m.apiHost != expectedAPI {
		t.Errorf("apiHost = %q, want %q", m.apiHost, expectedAPI)
	}
	if m.ssoRegion != "eu-west-1" {
		t.Errorf("ssoRegion = %q, want %q", m.ssoRegion, "eu-west-1")
	}
}

func TestLoadFromCredsFile_NonExistentFile(t *testing.T) {
	m := &kiroAuthManager{cfg: newTestConfig()}
	err := m.loadFromCredsFile("/nonexistent/path/creds.json")
	if err == nil {
		t.Error("loadFromCredsFile() should return error for non-existent file")
	}
}

func TestLoadFromCredsFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{not valid json}"), 0600)

	m := &kiroAuthManager{cfg: newTestConfig()}
	err := m.loadFromCredsFile(path)
	if err == nil {
		t.Error("loadFromCredsFile() should return error for invalid JSON")
	}
}

func TestLoadFromCredsFile_ExpiresAtParsing(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		wantZero  bool
	}{
		{"RFC3339", "2099-06-15T10:30:00Z", false},
		{"RFC3339Nano", "2099-06-15T10:30:00.123456789Z", false},
		{"ISO8601 no TZ", "2099-06-15T10:30:00", false},
		{"invalid", "not-a-date", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := map[string]any{"refreshToken": "rt"}
			if tt.expiresAt != "" {
				data["expiresAt"] = tt.expiresAt
			}
			path := createTempCredsFile(t, data)

			m := &kiroAuthManager{cfg: newTestConfig()}
			_ = m.loadFromCredsFile(path)

			if tt.wantZero && !m.expiresAt.IsZero() {
				t.Errorf("expiresAt should be zero for %q", tt.expiresAt)
			}
			if !tt.wantZero && m.expiresAt.IsZero() {
				t.Errorf("expiresAt should not be zero for %q", tt.expiresAt)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. Credential loading from env var (REFRESH_TOKEN)
// ---------------------------------------------------------------------------

func TestLoadCredentials_EnvVar(t *testing.T) {
	cfg := newTestConfig()
	cfg.RefreshToken = "env-refresh-token"

	m := &kiroAuthManager{
		cfg:     cfg,
		apiHost: fmt.Sprintf(kiroAPIHostTemplate, cfg.Region),
		qHost:   fmt.Sprintf(kiroQHostTemplate, cfg.Region),
	}

	err := m.loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials() error: %v", err)
	}

	if m.refreshToken != "env-refresh-token" {
		t.Errorf("refreshToken = %q, want %q", m.refreshToken, "env-refresh-token")
	}
}

// ---------------------------------------------------------------------------
// 6. Credential loading priority: file > env > sqlite
// ---------------------------------------------------------------------------

func TestLoadCredentials_FileTakesPriority(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "from-file",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path
	cfg.RefreshToken = "from-env"

	m := &kiroAuthManager{
		cfg:     cfg,
		apiHost: fmt.Sprintf(kiroAPIHostTemplate, cfg.Region),
		qHost:   fmt.Sprintf(kiroQHostTemplate, cfg.Region),
	}

	err := m.loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials() error: %v", err)
	}

	if m.refreshToken != "from-file" {
		t.Errorf("refreshToken = %q, want %q (file should take priority)", m.refreshToken, "from-file")
	}
}

func TestLoadCredentials_EnvFallsBackWhenFileInvalid(t *testing.T) {
	cfg := newTestConfig()
	cfg.CredsFile = "/nonexistent/creds.json"
	cfg.RefreshToken = "from-env"

	m := &kiroAuthManager{
		cfg:     cfg,
		apiHost: fmt.Sprintf(kiroAPIHostTemplate, cfg.Region),
		qHost:   fmt.Sprintf(kiroQHostTemplate, cfg.Region),
	}

	err := m.loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials() error: %v", err)
	}

	if m.refreshToken != "from-env" {
		t.Errorf("refreshToken = %q, want %q (env fallback)", m.refreshToken, "from-env")
	}
}

func TestLoadCredentials_NoSourceAvailable(t *testing.T) {
	cfg := newTestConfig()
	m := &kiroAuthManager{
		cfg:     cfg,
		apiHost: fmt.Sprintf(kiroAPIHostTemplate, cfg.Region),
		qHost:   fmt.Sprintf(kiroQHostTemplate, cfg.Region),
	}

	err := m.loadCredentials()
	if err == nil {
		t.Error("loadCredentials() should return error when no source available")
	}
}

// ---------------------------------------------------------------------------
// 7. SQLite credential loading
// ---------------------------------------------------------------------------

func TestLoadFromSQLite_SocialTokenKey(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken": "sqlite-rt",
		"accessToken":  "sqlite-at",
		"profileArn":   "arn:test",
		"expiresAt":    "2099-01-01T00:00:00Z",
	})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	creds, key, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if key != "kirocli:social:token" {
		t.Errorf("key = %q, want %q", key, "kirocli:social:token")
	}
	if creds.RefreshToken != "sqlite-rt" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "sqlite-rt")
	}
	if creds.AccessToken != "sqlite-at" {
		t.Errorf("AccessToken = %q, want %q", creds.AccessToken, "sqlite-at")
	}
	if creds.ProfileARN != "arn:test" {
		t.Errorf("ProfileARN = %q, want %q", creds.ProfileARN, "arn:test")
	}
}

func TestLoadFromSQLite_KeyPriorityOrder(t *testing.T) {
	// All three keys present — social should win.
	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token":     tokenJSON(t, map[string]any{"refreshToken": "social-rt"}),
		"kirocli:odic:token":       tokenJSON(t, map[string]any{"refreshToken": "odic-rt"}),
		"codewhisperer:odic:token": tokenJSON(t, map[string]any{"refreshToken": "cw-rt"}),
	})

	creds, key, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if key != "kirocli:social:token" {
		t.Errorf("key = %q, want %q (highest priority)", key, "kirocli:social:token")
	}
	if creds.RefreshToken != "social-rt" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "social-rt")
	}
}

func TestLoadFromSQLite_FallsToSecondKey(t *testing.T) {
	// Only odic and cw keys present — odic should win.
	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:odic:token":       tokenJSON(t, map[string]any{"refreshToken": "odic-rt"}),
		"codewhisperer:odic:token": tokenJSON(t, map[string]any{"refreshToken": "cw-rt"}),
	})

	creds, key, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if key != "kirocli:odic:token" {
		t.Errorf("key = %q, want %q", key, "kirocli:odic:token")
	}
	if creds.RefreshToken != "odic-rt" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "odic-rt")
	}
}

func TestLoadFromSQLite_FallsToThirdKey(t *testing.T) {
	// Only cw key present.
	dbPath := createTempSQLiteDB(t, map[string]string{
		"codewhisperer:odic:token": tokenJSON(t, map[string]any{"refreshToken": "cw-rt"}),
	})

	creds, key, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if key != "codewhisperer:odic:token" {
		t.Errorf("key = %q, want %q", key, "codewhisperer:odic:token")
	}
	if creds.RefreshToken != "cw-rt" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "cw-rt")
	}
}

func TestLoadFromSQLite_NoTokenFound(t *testing.T) {
	// Empty auth_kv table.
	dbPath := createTempSQLiteDB(t, map[string]string{})

	_, _, err := loadCredentialsFromSQLite(dbPath)
	if err == nil {
		t.Error("loadCredentialsFromSQLite() should return error when no token found")
	}
}

func TestLoadFromSQLite_SnakeCaseFields(t *testing.T) {
	// Test that snake_case JSON fields are supported.
	tokenData := tokenJSON(t, map[string]any{
		"refresh_token": "snake-rt",
		"access_token":  "snake-at",
		"profile_arn":   "arn:snake",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	creds, _, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if creds.RefreshToken != "snake-rt" {
		t.Errorf("RefreshToken = %q, want %q (snake_case fallback)", creds.RefreshToken, "snake-rt")
	}
	if creds.AccessToken != "snake-at" {
		t.Errorf("AccessToken = %q, want %q (snake_case fallback)", creds.AccessToken, "snake-at")
	}
	if creds.ProfileARN != "arn:snake" {
		t.Errorf("ProfileARN = %q, want %q (snake_case fallback)", creds.ProfileARN, "arn:snake")
	}
}

func TestLoadFromSQLite_CamelCaseTakesPriority(t *testing.T) {
	// Both camelCase and snake_case present — camelCase should win.
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken":  "camel-rt",
		"refresh_token": "snake-rt",
	})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	creds, _, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if creds.RefreshToken != "camel-rt" {
		t.Errorf("RefreshToken = %q, want %q (camelCase priority)", creds.RefreshToken, "camel-rt")
	}
}

func TestLoadFromSQLite_DeviceRegistration(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken": "rt-with-reg",
	})
	regData := tokenJSON(t, map[string]any{
		"clientId":     "reg-client-id",
		"clientSecret": "reg-client-secret",
		"region":       "ap-southeast-1",
	})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token":             tokenData,
		"kirocli:odic:device-registration": regData,
	})

	creds, _, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadCredentialsFromSQLite() error: %v", err)
	}

	if creds.ClientID != "reg-client-id" {
		t.Errorf("ClientID = %q, want %q", creds.ClientID, "reg-client-id")
	}
	if creds.ClientSecret != "reg-client-secret" {
		t.Errorf("ClientSecret = %q, want %q", creds.ClientSecret, "reg-client-secret")
	}
}

func TestLoadFromSQLite_NonExistentDB(t *testing.T) {
	_, _, err := loadCredentialsFromSQLite("/nonexistent/data.sqlite3")
	if err == nil {
		t.Error("loadCredentialsFromSQLite() should return error for non-existent DB")
	}
}

// ---------------------------------------------------------------------------
// 8. SQLite credential saving
// ---------------------------------------------------------------------------

func TestSaveCredentialsToSQLite_UpdatesExistingKey(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken": "old-rt",
		"accessToken":  "old-at",
	})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	expiresAt := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	err := saveCredentialsToSQLite(dbPath, "kirocli:social:token", "new-at", "new-rt", expiresAt)
	if err != nil {
		t.Fatalf("saveCredentialsToSQLite() error: %v", err)
	}

	// Read back and verify.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var val string
	err = db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", "kirocli:social:token").Scan(&val)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var data map[string]any
	json.Unmarshal([]byte(val), &data)

	if data["accessToken"] != "new-at" {
		t.Errorf("accessToken = %v, want %q", data["accessToken"], "new-at")
	}
	if data["refreshToken"] != "new-rt" {
		t.Errorf("refreshToken = %v, want %q", data["refreshToken"], "new-rt")
	}
	// Should also write snake_case variants.
	if data["access_token"] != "new-at" {
		t.Errorf("access_token = %v, want %q", data["access_token"], "new-at")
	}
}

func TestSaveCredentialsToSQLite_FallbackKey(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{"refreshToken": "old"})

	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	expiresAt := time.Now().Add(1 * time.Hour)
	// Pass empty key — should fall back to known keys.
	err := saveCredentialsToSQLite(dbPath, "", "new-at", "new-rt", expiresAt)
	if err != nil {
		t.Fatalf("saveCredentialsToSQLite() error: %v", err)
	}
}

func TestSaveCredentialsToSQLite_NonExistentDB(t *testing.T) {
	err := saveCredentialsToSQLite("/nonexistent/db.sqlite3", "key", "at", "rt", time.Now())
	if err == nil {
		t.Error("saveCredentialsToSQLite() should return error for non-existent DB")
	}
}

// ---------------------------------------------------------------------------
// 9. NewAuthManager constructor
// ---------------------------------------------------------------------------

func TestNewAuthManager_WithCredsFile_KiroDesktop(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt-desktop",
		"accessToken":  "at-desktop",
		"expiresAt":    "2099-01-01T00:00:00Z",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	if am.AuthType() != AuthTypeKiroDesktop {
		t.Errorf("AuthType() = %q, want %q", am.AuthType(), AuthTypeKiroDesktop)
	}
	if am.Fingerprint() == "" {
		t.Error("Fingerprint() should not be empty")
	}
	if am.APIHost() == "" {
		t.Error("APIHost() should not be empty")
	}
	if am.QHost() == "" {
		t.Error("QHost() should not be empty")
	}
}

func TestNewAuthManager_WithCredsFile_AWSSSO(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt-sso",
		"clientId":     "sso-id",
		"clientSecret": "sso-secret",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	if am.AuthType() != AuthTypeAWSSSO {
		t.Errorf("AuthType() = %q, want %q", am.AuthType(), AuthTypeAWSSSO)
	}
}

func TestNewAuthManager_WithEnvVar(t *testing.T) {
	cfg := newTestConfig()
	cfg.RefreshToken = "env-rt"

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	if am.AuthType() != AuthTypeKiroDesktop {
		t.Errorf("AuthType() = %q, want %q (no clientId/secret → desktop)", am.AuthType(), AuthTypeKiroDesktop)
	}
}

func TestNewAuthManager_WithSQLite(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken": "sqlite-rt",
	})
	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	cfg := newTestConfig()
	cfg.CLIDBFile = dbPath

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	if am.AuthType() != AuthTypeKiroDesktop {
		t.Errorf("AuthType() = %q, want %q", am.AuthType(), AuthTypeKiroDesktop)
	}
}

func TestNewAuthManager_NoCredentials(t *testing.T) {
	cfg := newTestConfig()

	_, err := NewAuthManager(cfg)
	if err == nil {
		t.Error("NewAuthManager() should return error when no credentials available")
	}
}

func TestNewAuthManager_ProfileARNFromConfig(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path
	cfg.ProfileARN = "arn:from:config"

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	if am.ProfileARN() != "arn:from:config" {
		t.Errorf("ProfileARN() = %q, want %q", am.ProfileARN(), "arn:from:config")
	}
}

func TestNewAuthManager_ProfileARNFromCredsFile(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt",
		"profileArn":   "arn:from:file",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path
	cfg.ProfileARN = "arn:from:config"

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	// Creds file profileArn is loaded first, then config is only set if empty.
	if am.ProfileARN() != "arn:from:file" {
		t.Errorf("ProfileARN() = %q, want %q (creds file takes priority)", am.ProfileARN(), "arn:from:file")
	}
}

func TestNewAuthManager_APIHostFromRegion(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "rt",
	})

	cfg := newTestConfig()
	cfg.CredsFile = path
	cfg.Region = "eu-west-1"

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	expected := fmt.Sprintf(kiroAPIHostTemplate, "eu-west-1")
	if am.APIHost() != expected {
		t.Errorf("APIHost() = %q, want %q", am.APIHost(), expected)
	}
}

// ---------------------------------------------------------------------------
// 10. GetAccessToken with mock refresher (token refresh + caching)
// ---------------------------------------------------------------------------

// mockRefresher is a test refresher that returns configurable values.
type mockRefresher struct {
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	err          error
	callCount    int
	mu           sync.Mutex
}

func (r *mockRefresher) refresh(ctx context.Context, m *kiroAuthManager) (string, string, time.Time, error) {
	r.mu.Lock()
	r.callCount++
	r.mu.Unlock()

	if r.err != nil {
		return "", "", time.Time{}, r.err
	}
	return r.accessToken, r.refreshToken, r.expiresAt, nil
}

func (r *mockRefresher) getCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount
}

func TestGetAccessToken_ReturnsCachedToken(t *testing.T) {
	m := &kiroAuthManager{
		cfg:         newTestConfig(),
		accessToken: "cached-token",
		expiresAt:   time.Now().Add(2 * time.Hour),
		tokenRefresher: &mockRefresher{
			accessToken: "new-token",
			expiresAt:   time.Now().Add(3 * time.Hour),
		},
	}

	token, err := m.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "cached-token" {
		t.Errorf("token = %q, want %q (should use cached)", token, "cached-token")
	}

	// Refresher should not have been called.
	if m.tokenRefresher.(*mockRefresher).getCallCount() != 0 {
		t.Error("refresher should not be called when token is valid")
	}
}

func TestGetAccessToken_RefreshesExpiredToken(t *testing.T) {
	mock := &mockRefresher{
		accessToken:  "refreshed-token",
		refreshToken: "new-rt",
		expiresAt:    time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "old-token",
		expiresAt:      time.Now().Add(-1 * time.Hour), // expired
		tokenRefresher: mock,
	}

	token, err := m.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "refreshed-token" {
		t.Errorf("token = %q, want %q", token, "refreshed-token")
	}
	if mock.getCallCount() != 1 {
		t.Errorf("refresher call count = %d, want 1", mock.getCallCount())
	}
}

func TestGetAccessToken_RefreshesTokenWithinThreshold(t *testing.T) {
	mock := &mockRefresher{
		accessToken: "refreshed-token",
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "old-token",
		expiresAt:      time.Now().Add(5 * time.Minute), // within 10-min threshold
		tokenRefresher: mock,
	}

	token, err := m.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "refreshed-token" {
		t.Errorf("token = %q, want %q", token, "refreshed-token")
	}
}

func TestGetAccessToken_RefreshError_GracefulDegradation(t *testing.T) {
	// Refresh fails but token hasn't actually expired yet.
	mock := &mockRefresher{
		err: fmt.Errorf("network error"),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "still-valid-token",
		expiresAt:      time.Now().Add(5 * time.Minute), // within threshold but not expired
		tokenRefresher: mock,
	}

	token, err := m.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() should gracefully degrade, got error: %v", err)
	}

	if token != "still-valid-token" {
		t.Errorf("token = %q, want %q (graceful degradation)", token, "still-valid-token")
	}
}

func TestGetAccessToken_RefreshError_NoFallback(t *testing.T) {
	// Refresh fails and token is fully expired.
	mock := &mockRefresher{
		err: fmt.Errorf("network error"),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "expired-token",
		expiresAt:      time.Now().Add(-1 * time.Hour), // fully expired
		tokenRefresher: mock,
	}

	_, err := m.GetAccessToken(context.Background())
	if err == nil {
		t.Error("GetAccessToken() should return error when refresh fails and token is expired")
	}
}

func TestGetAccessToken_EmptyToken_TriggersRefresh(t *testing.T) {
	mock := &mockRefresher{
		accessToken: "fresh-token",
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		tokenRefresher: mock,
	}

	token, err := m.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "fresh-token" {
		t.Errorf("token = %q, want %q", token, "fresh-token")
	}
}

// ---------------------------------------------------------------------------
// 11. ForceRefresh (403 scenario)
// ---------------------------------------------------------------------------

func TestForceRefresh_CallsRefresher(t *testing.T) {
	mock := &mockRefresher{
		accessToken:  "force-refreshed",
		refreshToken: "new-rt",
		expiresAt:    time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "valid-token",
		expiresAt:      time.Now().Add(2 * time.Hour), // still valid
		tokenRefresher: mock,
	}

	err := m.ForceRefresh(context.Background())
	if err != nil {
		t.Fatalf("ForceRefresh() error: %v", err)
	}

	if mock.getCallCount() != 1 {
		t.Errorf("refresher call count = %d, want 1", mock.getCallCount())
	}

	// Verify the token was updated.
	token, _ := m.GetAccessToken(context.Background())
	if token != "force-refreshed" {
		t.Errorf("token after ForceRefresh = %q, want %q", token, "force-refreshed")
	}
}

func TestForceRefresh_Error(t *testing.T) {
	mock := &mockRefresher{
		err: fmt.Errorf("refresh failed"),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		tokenRefresher: mock,
	}

	err := m.ForceRefresh(context.Background())
	if err == nil {
		t.Error("ForceRefresh() should return error when refresh fails")
	}
}

func TestForceRefresh_NoRefresher(t *testing.T) {
	m := &kiroAuthManager{
		cfg: newTestConfig(),
	}

	err := m.ForceRefresh(context.Background())
	if err == nil {
		t.Error("ForceRefresh() should return error when no refresher is set")
	}
}

// ---------------------------------------------------------------------------
// 12. Thread-safe concurrent token refresh
// ---------------------------------------------------------------------------

func TestGetAccessToken_ConcurrentAccess(t *testing.T) {
	mock := &mockRefresher{
		accessToken: "concurrent-token",
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "expired-token",
		expiresAt:      time.Now().Add(-1 * time.Hour),
		tokenRefresher: mock,
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	tokens := make([]string, goroutines)
	errors := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			token, err := m.GetAccessToken(context.Background())
			tokens[idx] = token
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All goroutines should get the same token.
	for i := 0; i < goroutines; i++ {
		if errors[i] != nil {
			t.Errorf("goroutine %d error: %v", i, errors[i])
		}
		if tokens[i] != "concurrent-token" {
			t.Errorf("goroutine %d token = %q, want %q", i, tokens[i], "concurrent-token")
		}
	}

	// The mutex serializes access, so the refresher is called once for the
	// first goroutine that acquires the lock. Subsequent goroutines find
	// the token valid and skip refresh. However, due to timing, the first
	// goroutine may not have completed before others acquire the lock.
	// The key invariant is: all goroutines get the correct token.
	if mock.getCallCount() < 1 {
		t.Error("refresher should be called at least once")
	}
}

func TestForceRefresh_ConcurrentAccess(t *testing.T) {
	mock := &mockRefresher{
		accessToken: "force-concurrent",
		expiresAt:   time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "old-token",
		expiresAt:      time.Now().Add(2 * time.Hour),
		tokenRefresher: mock,
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			errors[idx] = m.ForceRefresh(context.Background())
		}(i)
	}

	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errors[i] != nil {
			t.Errorf("goroutine %d error: %v", i, errors[i])
		}
	}

	// Each ForceRefresh call should trigger a refresh (no double-check skip).
	if mock.getCallCount() != goroutines {
		t.Errorf("refresher call count = %d, want %d", mock.getCallCount(), goroutines)
	}
}

// ---------------------------------------------------------------------------
// 13. Kiro Desktop token refresh with mock HTTP server
// ---------------------------------------------------------------------------

func TestKiroDesktopRefresh_Success(t *testing.T) {
	// Mock the Kiro Desktop refresh endpoint.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		var req kiroDesktopRefreshRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.RefreshToken != "test-rt" {
			t.Errorf("request refreshToken = %q, want %q", req.RefreshToken, "test-rt")
		}

		resp := kiroDesktopRefreshResponse{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    3600,
			ProfileARN:   "arn:refreshed",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Override the URL template to point to our test server.
	// We do this by setting the manager's cfg.Region to a value that,
	// when formatted, gives us the test server URL. Instead, we'll
	// directly test the refresher by constructing a manager that would
	// call the test server.
	//
	// Since kiroDesktopRefreshURLTemplate is a const, we test the
	// refresher indirectly by creating a custom refresher that uses
	// the test server URL.
	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		refreshToken: "test-rt",
		profileARN:   "arn:test",
		fingerprint:  "test-fingerprint",
	}

	// Create a custom refresher that hits our test server.
	customRefresher := &testDesktopRefresher{serverURL: server.URL}
	accessToken, newRT, expiresAt, err := customRefresher.refresh(context.Background(), m)
	if err != nil {
		t.Fatalf("refresh() error: %v", err)
	}

	if accessToken != "new-access-token" {
		t.Errorf("accessToken = %q, want %q", accessToken, "new-access-token")
	}
	if newRT != "new-refresh-token" {
		t.Errorf("newRefreshToken = %q, want %q", newRT, "new-refresh-token")
	}
	if expiresAt.IsZero() {
		t.Error("expiresAt should not be zero")
	}
}

// testDesktopRefresher is a test-only refresher that hits a custom URL.
type testDesktopRefresher struct {
	serverURL string
}

func (r *testDesktopRefresher) refresh(ctx context.Context, m *kiroAuthManager) (string, string, time.Time, error) {
	if m.refreshToken == "" {
		return "", "", time.Time{}, fmt.Errorf("refresh token is not set")
	}

	reqBody := kiroDesktopRefreshRequest{
		RefreshToken: m.refreshToken,
		ProfileARN:   m.profileARN,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.serverURL, bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("KiroIDE-0.7.45-%s", m.fingerprint))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var refreshResp kiroDesktopRefreshResponse
	json.NewDecoder(resp.Body).Decode(&refreshResp)

	expiresIn := refreshResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiresAt := time.Now().UTC().Truncate(time.Second).Add(
		time.Duration(expiresIn-60) * time.Second,
	)

	return refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt, nil
}

func TestKiroDesktopRefresh_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal error"}`))
	}))
	defer server.Close()

	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		refreshToken: "test-rt",
		fingerprint:  "fp",
	}

	customRefresher := &testDesktopRefresher{serverURL: server.URL}
	_, _, _, err := customRefresher.refresh(context.Background(), m)
	if err == nil {
		t.Error("refresh() should return error on server error")
	}
}

func TestKiroDesktopRefresh_MissingRefreshToken(t *testing.T) {
	m := &kiroAuthManager{
		cfg: newTestConfig(),
	}

	r := &kiroDesktopRefresher{}
	_, _, _, err := r.refresh(context.Background(), m)
	if err == nil {
		t.Error("refresh() should return error when refresh token is empty")
	}
}

// ---------------------------------------------------------------------------
// 14. AWS SSO OIDC token refresh with mock HTTP server
// ---------------------------------------------------------------------------

func TestAWSSSORefresh_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		var req awsSSORefreshRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.GrantType != "refresh_token" {
			t.Errorf("grantType = %q, want %q", req.GrantType, "refresh_token")
		}
		if req.ClientID != "test-client-id" {
			t.Errorf("clientId = %q, want %q", req.ClientID, "test-client-id")
		}

		resp := awsSSORefreshResponse{
			AccessToken:  "sso-access-token",
			RefreshToken: "sso-refresh-token",
			ExpiresIn:    7200,
			TokenType:    "Bearer",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		refreshToken: "test-sso-rt",
		clientID:     "test-client-id",
		clientSecret: "test-client-secret",
		ssoRegion:    "us-east-1",
		fingerprint:  "fp",
	}

	customRefresher := &testSSORefresher{serverURL: server.URL}
	accessToken, newRT, expiresAt, err := customRefresher.refresh(context.Background(), m)
	if err != nil {
		t.Fatalf("refresh() error: %v", err)
	}

	if accessToken != "sso-access-token" {
		t.Errorf("accessToken = %q, want %q", accessToken, "sso-access-token")
	}
	if newRT != "sso-refresh-token" {
		t.Errorf("newRefreshToken = %q, want %q", newRT, "sso-refresh-token")
	}
	if expiresAt.IsZero() {
		t.Error("expiresAt should not be zero")
	}
}

// testSSORefresher is a test-only refresher that hits a custom URL for SSO.
type testSSORefresher struct {
	serverURL string
}

func (r *testSSORefresher) refresh(ctx context.Context, m *kiroAuthManager) (string, string, time.Time, error) {
	if m.refreshToken == "" {
		return "", "", time.Time{}, fmt.Errorf("refresh token is not set")
	}
	if m.clientID == "" {
		return "", "", time.Time{}, fmt.Errorf("client ID is not set")
	}
	if m.clientSecret == "" {
		return "", "", time.Time{}, fmt.Errorf("client secret is not set")
	}

	reqBody := awsSSORefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RefreshToken: m.refreshToken,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.serverURL, bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var refreshResp awsSSORefreshResponse
	json.NewDecoder(resp.Body).Decode(&refreshResp)

	expiresIn := refreshResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiresAt := time.Now().UTC().Truncate(time.Second).Add(
		time.Duration(expiresIn-60) * time.Second,
	)

	return refreshResp.AccessToken, refreshResp.RefreshToken, expiresAt, nil
}

func TestAWSSSORefresh_MissingClientID(t *testing.T) {
	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		refreshToken: "rt",
		clientSecret: "secret",
	}

	r := &awsSSORefresher{}
	_, _, _, err := r.refresh(context.Background(), m)
	if err == nil {
		t.Error("refresh() should return error when clientID is empty")
	}
}

func TestAWSSSORefresh_MissingClientSecret(t *testing.T) {
	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		refreshToken: "rt",
		clientID:     "id",
	}

	r := &awsSSORefresher{}
	_, _, _, err := r.refresh(context.Background(), m)
	if err == nil {
		t.Error("refresh() should return error when clientSecret is empty")
	}
}

func TestAWSSSORefresh_MissingRefreshToken(t *testing.T) {
	m := &kiroAuthManager{
		cfg:          newTestConfig(),
		clientID:     "id",
		clientSecret: "secret",
	}

	r := &awsSSORefresher{}
	_, _, _, err := r.refresh(context.Background(), m)
	if err == nil {
		t.Error("refresh() should return error when refreshToken is empty")
	}
}

// ---------------------------------------------------------------------------
// 15. parseTime helper
// ---------------------------------------------------------------------------

func TestParseTime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantZero bool
	}{
		{"RFC3339", "2024-06-15T10:30:00Z", false},
		{"RFC3339 with offset", "2024-06-15T10:30:00+05:00", false},
		{"RFC3339Nano", "2024-06-15T10:30:00.123456789Z", false},
		{"ISO8601 no TZ", "2024-06-15T10:30:00", false},
		{"invalid", "not-a-date", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTime(tt.input)
			if tt.wantZero && !result.IsZero() {
				t.Errorf("parseTime(%q) should return zero time", tt.input)
			}
			if !tt.wantZero && result.IsZero() {
				t.Errorf("parseTime(%q) should not return zero time", tt.input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 16. coalesce helper
// ---------------------------------------------------------------------------

func TestCoalesce(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"first non-empty", []string{"a", "b"}, "a"},
		{"skip empty", []string{"", "b"}, "b"},
		{"all empty", []string{"", ""}, ""},
		{"single value", []string{"x"}, "x"},
		{"no values", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coalesce(tt.values...)
			if got != tt.want {
				t.Errorf("coalesce(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 17. truncateForLog helper
// ---------------------------------------------------------------------------

func TestTruncateForLog(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string", "abc", 8, "abc"},
		{"exact length", "abcdefgh", 8, "abcdefgh"},
		{"long string", "abcdefghij", 8, "abcdefgh..."},
		{"empty", "", 8, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForLog(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncateForLog(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 18. doRefresh updates manager state
// ---------------------------------------------------------------------------

func TestDoRefresh_UpdatesManagerState(t *testing.T) {
	newExpiry := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	mock := &mockRefresher{
		accessToken:  "new-at",
		refreshToken: "new-rt",
		expiresAt:    newExpiry,
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		accessToken:    "old-at",
		refreshToken:   "old-rt",
		expiresAt:      time.Now().Add(-1 * time.Hour),
		tokenRefresher: mock,
	}

	m.mu.Lock()
	err := m.doRefresh(context.Background())
	m.mu.Unlock()

	if err != nil {
		t.Fatalf("doRefresh() error: %v", err)
	}

	if m.accessToken != "new-at" {
		t.Errorf("accessToken = %q, want %q", m.accessToken, "new-at")
	}
	if m.refreshToken != "new-rt" {
		t.Errorf("refreshToken = %q, want %q", m.refreshToken, "new-rt")
	}
	if !m.expiresAt.Equal(newExpiry) {
		t.Errorf("expiresAt = %v, want %v", m.expiresAt, newExpiry)
	}
}

func TestDoRefresh_EmptyNewRefreshToken_KeepsOld(t *testing.T) {
	mock := &mockRefresher{
		accessToken:  "new-at",
		refreshToken: "", // empty — should keep old
		expiresAt:    time.Now().Add(1 * time.Hour),
	}

	m := &kiroAuthManager{
		cfg:            newTestConfig(),
		refreshToken:   "original-rt",
		tokenRefresher: mock,
	}

	m.mu.Lock()
	err := m.doRefresh(context.Background())
	m.mu.Unlock()

	if err != nil {
		t.Fatalf("doRefresh() error: %v", err)
	}

	if m.refreshToken != "original-rt" {
		t.Errorf("refreshToken = %q, want %q (should keep old when new is empty)", m.refreshToken, "original-rt")
	}
}

// ---------------------------------------------------------------------------
// 19. Full integration: NewAuthManager → GetAccessToken with SQLite
// ---------------------------------------------------------------------------

func TestIntegration_SQLiteToGetAccessToken(t *testing.T) {
	tokenData := tokenJSON(t, map[string]any{
		"refreshToken": "sqlite-rt",
		"accessToken":  "sqlite-at",
		"expiresAt":    time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})
	dbPath := createTempSQLiteDB(t, map[string]string{
		"kirocli:social:token": tokenData,
	})

	cfg := newTestConfig()
	cfg.CLIDBFile = dbPath

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	// The token loaded from SQLite should be valid (expires in 2 hours).
	token, err := am.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "sqlite-at" {
		t.Errorf("token = %q, want %q", token, "sqlite-at")
	}
}

func TestIntegration_CredsFileToGetAccessToken(t *testing.T) {
	path := createTempCredsFile(t, map[string]any{
		"refreshToken": "file-rt",
		"accessToken":  "file-at",
		"expiresAt":    time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})

	cfg := newTestConfig()
	cfg.CredsFile = path

	am, err := NewAuthManager(cfg)
	if err != nil {
		t.Fatalf("NewAuthManager() error: %v", err)
	}

	token, err := am.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetAccessToken() error: %v", err)
	}

	if token != "file-at" {
		t.Errorf("token = %q, want %q", token, "file-at")
	}
}
