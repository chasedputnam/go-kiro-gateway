// Package auth manages the lifecycle of access tokens for the Kiro API.
//
// It supports four credential sources (in priority order):
//  1. JSON file (KIRO_CREDS_FILE) — exported from Kiro IDE
//  2. Environment variable (REFRESH_TOKEN) — direct refresh token
//  3. SQLite database (KIRO_CLI_DB_FILE) — kiro-cli credentials
//  4. Enterprise Kiro IDE — device registration from ~/.aws/sso/cache/{clientIdHash}.json
//
// Auth type is auto-detected: presence of clientId + clientSecret → AWS SSO OIDC,
// otherwise → Kiro Desktop.
//
// Token refresh is mutex-protected with a double-check pattern so that concurrent
// goroutines share a single refresh rather than racing.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/user"
	"sync"
	"time"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
)

// ---------------------------------------------------------------------------
// AuthType enum
// ---------------------------------------------------------------------------

// AuthType identifies the authentication mechanism in use.
type AuthType string

const (
	// AuthTypeKiroDesktop uses the Kiro Desktop Auth refresh endpoint.
	AuthTypeKiroDesktop AuthType = "kiro_desktop"
	// AuthTypeAWSSSO uses the AWS SSO OIDC token endpoint.
	AuthTypeAWSSSO AuthType = "aws_sso"
)

// ---------------------------------------------------------------------------
// AuthManager interface
// ---------------------------------------------------------------------------

// AuthManager abstracts token lifecycle management so that callers (HTTP
// client, route handlers) do not need to know about credential sources or
// refresh mechanics.
type AuthManager interface {
	// GetAccessToken returns a valid access token, refreshing if needed.
	// The implementation is thread-safe.
	GetAccessToken(ctx context.Context) (string, error)

	// ForceRefresh forces a token refresh regardless of expiration.
	// Used when a 403 is received from the Kiro API.
	ForceRefresh(ctx context.Context) error

	// AuthType returns the detected authentication type.
	AuthType() AuthType

	// ProfileARN returns the AWS CodeWhisperer profile ARN.
	// Empty for AWS SSO OIDC auth.
	ProfileARN() string

	// Fingerprint returns the machine fingerprint for User-Agent headers.
	Fingerprint() string

	// APIHost returns the Kiro API host URL for the configured region.
	APIHost() string

	// QHost returns the Q API host URL for the configured region.
	QHost() string
}

// ---------------------------------------------------------------------------
// refresher — pluggable token refresh strategy
// ---------------------------------------------------------------------------

// refresher performs the actual HTTP token refresh. Implementations live in
// kiro_desktop.go and aws_sso.go. This indirection lets us build and test
// the core auth logic without the HTTP plumbing (tasks 3.2 / 3.3).
type refresher interface {
	// refresh performs a token refresh and returns the new access token,
	// an optional new refresh token, and the expiration time.
	refresh(ctx context.Context, m *kiroAuthManager) (accessToken, newRefreshToken string, expiresAt time.Time, err error)
}

// ---------------------------------------------------------------------------
// Credential file structures
// ---------------------------------------------------------------------------

// credsFileData represents the JSON structure of a KIRO_CREDS_FILE.
type credsFileData struct {
	RefreshToken string `json:"refreshToken"`
	AccessToken  string `json:"accessToken"`
	ProfileARN   string `json:"profileArn"`
	Region       string `json:"region"`
	ExpiresAt    string `json:"expiresAt"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	ClientIDHash string `json:"clientIdHash"`
}

// deviceRegistration represents the JSON structure of an Enterprise Kiro IDE
// device registration file (~/.aws/sso/cache/{clientIdHash}.json).
type deviceRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// ---------------------------------------------------------------------------
// kiroAuthManager — concrete implementation
// ---------------------------------------------------------------------------

// kiroAuthManager implements AuthManager. All mutable token state is guarded
// by mu.
type kiroAuthManager struct {
	mu sync.Mutex

	cfg *config.Config

	// Credential state
	refreshToken string
	accessToken  string
	expiresAt    time.Time
	authType     AuthType
	fingerprint  string
	profileARN   string

	// AWS SSO OIDC specific
	clientID     string
	clientSecret string
	ssoRegion    string // may differ from API region

	// Derived hosts
	apiHost string
	qHost   string

	// Source tracking — which credential source was used
	credsFile      string // path to JSON creds file (if used)
	sqliteDB       string // path to SQLite DB (if used)
	sqliteTokenKey string // which key was loaded from SQLite

	// Pluggable refresh strategy (set after auth type detection)
	tokenRefresher refresher
}

// ---------------------------------------------------------------------------
// URL templates (matching Python config.py)
// ---------------------------------------------------------------------------

const (
	kiroAPIHostTemplate = "https://q.%s.amazonaws.com"
	kiroQHostTemplate   = "https://q.%s.amazonaws.com"
)

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewAuthManager creates a fully initialised AuthManager. It loads credentials
// based on the priority chain (file → env → sqlite), auto-detects the auth
// type, generates a machine fingerprint, and derives API hosts from the
// configured region.
func NewAuthManager(cfg *config.Config) (AuthManager, error) {
	m := &kiroAuthManager{
		cfg:       cfg,
		apiHost:   fmt.Sprintf(kiroAPIHostTemplate, cfg.Region),
		qHost:     fmt.Sprintf(kiroQHostTemplate, cfg.Region),
		ssoRegion: cfg.Region,
	}

	// 1. Generate machine fingerprint.
	m.fingerprint = generateFingerprint()

	// 2. Load credentials (priority: file → env → sqlite).
	if err := m.loadCredentials(); err != nil {
		return nil, fmt.Errorf("auth: failed to load credentials: %w", err)
	}

	// 3. Auto-detect auth type.
	m.detectAuthType()

	// 4. Wire the appropriate token refresher based on auth type.
	switch m.authType {
	case AuthTypeKiroDesktop:
		m.tokenRefresher = &kiroDesktopRefresher{}
	case AuthTypeAWSSSO:
		m.tokenRefresher = &awsSSORefresher{}
	}

	// 5. Set profile ARN from config if not loaded from creds.
	if m.profileARN == "" && cfg.ProfileARN != "" {
		m.profileARN = cfg.ProfileARN
	}

	log.Printf("Auth manager initialized: type=%s, region=%s, api_host=%s, q_host=%s, profileARN=%s",
		m.authType, cfg.Region, m.apiHost, m.qHost, m.profileARN)

	return m, nil
}

// ---------------------------------------------------------------------------
// AuthManager interface methods
// ---------------------------------------------------------------------------

// GetAccessToken returns a cached token if still valid, otherwise refreshes.
// Uses a mutex-protected double-check pattern: the lock is acquired, the token
// is re-checked (another goroutine may have refreshed while we waited), and
// only then is a refresh attempted.
func (m *kiroAuthManager) GetAccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isTokenValid() {
		return m.accessToken, nil
	}

	if err := m.doRefresh(ctx); err != nil {
		// Graceful degradation: if refresh fails but the token hasn't
		// actually expired yet, return it anyway.
		if m.accessToken != "" && time.Now().Before(m.expiresAt) {
			log.Printf("Token refresh failed but existing token still valid, using it: %v", err)
			return m.accessToken, nil
		}
		return "", fmt.Errorf("auth: token refresh failed: %w", err)
	}

	return m.accessToken, nil
}

// ForceRefresh forces a token refresh regardless of expiration. Called by the
// HTTP client on 403 responses.
func (m *kiroAuthManager) ForceRefresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.doRefresh(ctx)
}

// AuthType returns the detected authentication type.
func (m *kiroAuthManager) AuthType() AuthType { return m.authType }

// ProfileARN returns the profile ARN (empty for SSO OIDC).
func (m *kiroAuthManager) ProfileARN() string { return m.profileARN }

// Fingerprint returns the machine fingerprint.
func (m *kiroAuthManager) Fingerprint() string { return m.fingerprint }

// APIHost returns the Kiro API host URL.
func (m *kiroAuthManager) APIHost() string { return m.apiHost }

// QHost returns the Q API host URL.
func (m *kiroAuthManager) QHost() string { return m.qHost }

// ---------------------------------------------------------------------------
// Token validity check
// ---------------------------------------------------------------------------

// isTokenValid returns true when the cached access token exists and is not
// within the refresh threshold of expiration. Must be called under mu.Lock().
func (m *kiroAuthManager) isTokenValid() bool {
	if m.accessToken == "" {
		return false
	}
	if m.expiresAt.IsZero() {
		return false
	}
	return time.Until(m.expiresAt) > m.cfg.TokenRefreshThreshold
}

// ---------------------------------------------------------------------------
// Refresh dispatch
// ---------------------------------------------------------------------------

// doRefresh delegates to the pluggable refresher. Must be called under
// mu.Lock(). If no refresher is set (tasks 3.2/3.3 not yet wired), returns
// an error indicating the refresh path is not implemented.
func (m *kiroAuthManager) doRefresh(ctx context.Context) error {
	if m.tokenRefresher == nil {
		return fmt.Errorf("token refresh not implemented for auth type %s (pending tasks 3.2/3.3)", m.authType)
	}

	accessToken, newRefreshToken, expiresAt, err := m.tokenRefresher.refresh(ctx, m)
	if err != nil {
		return err
	}

	m.accessToken = accessToken
	if newRefreshToken != "" {
		m.refreshToken = newRefreshToken
	}
	m.expiresAt = expiresAt

	return nil
}

// ---------------------------------------------------------------------------
// Credential loading
// ---------------------------------------------------------------------------

// loadCredentials loads credentials from the first available source:
//  1. KIRO_CREDS_FILE (JSON file)
//  2. REFRESH_TOKEN (env var, already in cfg)
//  3. KIRO_CLI_DB_FILE (SQLite — placeholder, task 3.4)
func (m *kiroAuthManager) loadCredentials() error {
	// Priority 1: JSON credentials file.
	if m.cfg.CredsFile != "" {
		if err := m.loadFromCredsFile(m.cfg.CredsFile); err != nil {
			log.Printf("Warning: failed to load credentials from file %s: %v", m.cfg.CredsFile, err)
		} else {
			m.credsFile = m.cfg.CredsFile
			log.Printf("Credentials loaded from file: %s", m.cfg.CredsFile)
			return nil
		}
	}

	// Priority 2: REFRESH_TOKEN environment variable.
	if m.cfg.RefreshToken != "" {
		m.refreshToken = m.cfg.RefreshToken
		log.Printf("Credentials loaded from REFRESH_TOKEN environment variable")
		return nil
	}

	// Priority 3: SQLite database (kiro-cli).
	if m.cfg.CLIDBFile != "" {
		if err := m.loadFromSQLite(m.cfg.CLIDBFile); err != nil {
			log.Printf("Warning: failed to load credentials from SQLite %s: %v", m.cfg.CLIDBFile, err)
		} else {
			m.sqliteDB = m.cfg.CLIDBFile
			log.Printf("Credentials loaded from SQLite database: %s", m.cfg.CLIDBFile)
			return nil
		}
	}

	return fmt.Errorf("no credential source available")
}

// loadFromCredsFile reads a JSON credentials file and populates the manager
// fields. Supports both Kiro Desktop and AWS SSO OIDC credential formats,
// as well as Enterprise Kiro IDE (clientIdHash).
func (m *kiroAuthManager) loadFromCredsFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading creds file: %w", err)
	}

	var creds credsFileData
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parsing creds file: %w", err)
	}

	if creds.RefreshToken != "" {
		m.refreshToken = creds.RefreshToken
	}
	if creds.AccessToken != "" {
		m.accessToken = creds.AccessToken
	}
	if creds.ProfileARN != "" {
		m.profileARN = creds.ProfileARN
	}
	if creds.Region != "" {
		m.apiHost = fmt.Sprintf(kiroAPIHostTemplate, creds.Region)
		m.qHost = fmt.Sprintf(kiroQHostTemplate, creds.Region)
		m.ssoRegion = creds.Region
		log.Printf("Region updated from credentials file: region=%s, api_host=%s, q_host=%s, profileARN=%s",
			creds.Region, m.apiHost, m.qHost, m.profileARN)
	}

	// Enterprise Kiro IDE: load device registration from
	// ~/.aws/sso/cache/{clientIdHash}.json
	if creds.ClientIDHash != "" {
		m.loadEnterpriseDeviceRegistration(creds.ClientIDHash)
	}

	// Direct clientId/clientSecret in the creds file.
	if creds.ClientID != "" {
		m.clientID = creds.ClientID
	}
	if creds.ClientSecret != "" {
		m.clientSecret = creds.ClientSecret
	}

	// Parse expiresAt (ISO 8601 / RFC 3339).
	if creds.ExpiresAt != "" {
		m.expiresAt = parseTime(creds.ExpiresAt)
	}

	log.Printf("Auth state after credential load: authType=%s region=%s apiHost=%s qHost=%s profileARN=%s "+
		"hasRefreshToken=%t hasAccessToken=%t hasClientID=%t hasClientSecret=%t "+
		"expiresAt=%s fingerprint=%s credsFile=%s sqliteDB=%s",
		m.authType, m.ssoRegion, m.apiHost, m.qHost, m.profileARN,
		m.refreshToken != "", m.accessToken != "", m.clientID != "", m.clientSecret != "",
		m.expiresAt.Format(time.RFC3339), m.fingerprint, m.credsFile, m.sqliteDB)

	return nil
}

// loadEnterpriseDeviceRegistration loads clientId and clientSecret from the
// Enterprise Kiro IDE device registration file at
// ~/.aws/sso/cache/{clientIdHash}.json.
func (m *kiroAuthManager) loadEnterpriseDeviceRegistration(clientIDHash string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: cannot determine home directory for enterprise device registration: %v", err)
		return
	}

	path := fmt.Sprintf("%s/.aws/sso/cache/%s.json", home, clientIDHash)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Warning: enterprise device registration file not found: %s", path)
		return
	}

	var reg deviceRegistration
	if err := json.Unmarshal(data, &reg); err != nil {
		log.Printf("Warning: failed to parse enterprise device registration: %v", err)
		return
	}

	if reg.ClientID != "" {
		m.clientID = reg.ClientID
	}
	if reg.ClientSecret != "" {
		m.clientSecret = reg.ClientSecret
	}

	log.Printf("Enterprise device registration loaded from %s", path)
}

// loadFromSQLite loads credentials from a kiro-cli SQLite database.
// Delegates to loadCredentialsFromSQLite in sqlite.go which reads the
// auth_kv table with key priority and parses JSON token data.
func (m *kiroAuthManager) loadFromSQLite(dbPath string) error {
	creds, tokenKey, err := loadCredentialsFromSQLite(dbPath)
	if err != nil {
		return err
	}

	// Populate manager fields from the loaded credentials.
	if creds.RefreshToken != "" {
		m.refreshToken = creds.RefreshToken
	}
	if creds.AccessToken != "" {
		m.accessToken = creds.AccessToken
	}
	if creds.ProfileARN != "" {
		m.profileARN = creds.ProfileARN
	}
	if creds.ClientID != "" {
		m.clientID = creds.ClientID
	}
	if creds.ClientSecret != "" {
		m.clientSecret = creds.ClientSecret
	}

	// Store the SSO region from SQLite separately — it may differ from the
	// API region (e.g., SSO in ap-southeast-1 while API is us-east-1).
	// We do NOT update apiHost/qHost because the Kiro API is only in us-east-1.
	if creds.Region != "" {
		m.ssoRegion = creds.Region
		log.Printf("SSO region from SQLite: %s (API stays at %s)", creds.Region, m.cfg.Region)
	}

	// Parse expiresAt timestamp.
	if creds.ExpiresAt != "" {
		m.expiresAt = parseTime(creds.ExpiresAt)
	}

	// Remember which key was loaded so we can save back to the same one.
	m.sqliteTokenKey = tokenKey

	return nil
}

// ---------------------------------------------------------------------------
// Auth type detection
// ---------------------------------------------------------------------------

// detectAuthType sets authType based on the presence of clientId and
// clientSecret. If both are present → AWS SSO OIDC, otherwise → Kiro Desktop.
func (m *kiroAuthManager) detectAuthType() {
	if m.clientID != "" && m.clientSecret != "" {
		m.authType = AuthTypeAWSSSO
		log.Printf("Detected auth type: AWS SSO OIDC")
	} else {
		m.authType = AuthTypeKiroDesktop
		log.Printf("Detected auth type: Kiro Desktop")
	}
}

// ---------------------------------------------------------------------------
// Machine fingerprint
// ---------------------------------------------------------------------------

// generateFingerprint creates a deterministic SHA-256 fingerprint from the
// machine's hostname and current username, matching the Python implementation
// in kiro/utils.py.
func generateFingerprint() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	unique := fmt.Sprintf("%s-%s-kiro-gateway", hostname, username)
	hash := sha256.Sum256([]byte(unique))
	return fmt.Sprintf("%x", hash)
}

// ---------------------------------------------------------------------------
// Time parsing helper
// ---------------------------------------------------------------------------

// parseTime attempts to parse an ISO 8601 / RFC 3339 timestamp. Returns the
// zero time on failure.
func parseTime(s string) time.Time {
	// Try RFC 3339 first (most common).
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Try RFC 3339 with nanoseconds.
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	// Try basic ISO 8601 without timezone (assume UTC).
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC()
	}
	log.Printf("Warning: failed to parse expiresAt timestamp: %s", s)
	return time.Time{}
}
