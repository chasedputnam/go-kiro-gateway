package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper: clearConfigEnvVars unsets all env vars that Load() reads so tests
// start from a clean slate. t.Setenv already restores values after the test,
// but we also need to make sure no leaked vars from the host affect defaults.
// ---------------------------------------------------------------------------

func clearConfigEnvVars(t *testing.T) {
	t.Helper()
	vars := []string{
		"SERVER_HOST", "SERVER_PORT",
		"PROXY_API_KEY", "REFRESH_TOKEN", "PROFILE_ARN",
		"KIRO_REGION", "KIRO_CREDS_FILE", "KIRO_CLI_DB_FILE",
		"VPN_PROXY_URL",
		"FIRST_TOKEN_TIMEOUT", "FIRST_TOKEN_MAX_RETRIES", "STREAMING_READ_TIMEOUT",
		"FAKE_REASONING", "FAKE_REASONING_MAX_TOKENS",
		"FAKE_REASONING_HANDLING", "FAKE_REASONING_INITIAL_BUFFER_SIZE",
		"FAKE_REASONING_OPEN_TAGS",
		"TRUNCATION_RECOVERY",
		"DEBUG_MODE", "DEBUG_DIR",
		"LOG_LEVEL",
		"HIDDEN_MODELS", "MODEL_ALIASES", "HIDDEN_FROM_LIST",
		"TOOL_DESCRIPTION_MAX_LENGTH", "MODEL_CACHE_TTL",
		"DEFAULT_MAX_INPUT_TOKENS", "MAX_RETRIES", "BASE_RETRY_DELAY",
		"TOKEN_REFRESH_THRESHOLD",
	}
	for _, v := range vars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

// setCredentialSource sets a minimal credential so validation passes.
func setCredentialSource(t *testing.T) {
	t.Helper()
	t.Setenv("REFRESH_TOKEN", "test-token-abc")
}

// ---------------------------------------------------------------------------
// 1. Default values
// ---------------------------------------------------------------------------

func TestLoad_DefaultValues(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	// Override os.Args so applyCLIFlags doesn't pick up test runner flags.
	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	// Server defaults
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != 8000 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8000)
	}

	// Auth defaults
	if cfg.ProxyAPIKey != "my-super-secret-password-123" {
		t.Errorf("ProxyAPIKey = %q, want default", cfg.ProxyAPIKey)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", cfg.Region, "us-east-1")
	}

	// Timeout defaults
	if cfg.FirstTokenTimeout != 30*time.Second {
		t.Errorf("FirstTokenTimeout = %v, want 30s", cfg.FirstTokenTimeout)
	}
	if cfg.FirstTokenMaxRetries != 3 {
		t.Errorf("FirstTokenMaxRetries = %d, want 3", cfg.FirstTokenMaxRetries)
	}
	if cfg.StreamingReadTimeout != 300*time.Second {
		t.Errorf("StreamingReadTimeout = %v, want 300s", cfg.StreamingReadTimeout)
	}

	// Fake reasoning defaults
	if !cfg.FakeReasoningEnabled {
		t.Error("FakeReasoningEnabled = false, want true")
	}
	if cfg.FakeReasoningMaxTokens != 4000 {
		t.Errorf("FakeReasoningMaxTokens = %d, want 4000", cfg.FakeReasoningMaxTokens)
	}
	if cfg.FakeReasoningHandling != "as_reasoning_content" {
		t.Errorf("FakeReasoningHandling = %q, want %q", cfg.FakeReasoningHandling, "as_reasoning_content")
	}
	if cfg.FakeReasoningInitialBuffer != 20 {
		t.Errorf("FakeReasoningInitialBuffer = %d, want 20", cfg.FakeReasoningInitialBuffer)
	}
	if len(cfg.FakeReasoningOpenTags) != 4 {
		t.Errorf("FakeReasoningOpenTags len = %d, want 4", len(cfg.FakeReasoningOpenTags))
	}

	// Truncation default
	if !cfg.TruncationRecovery {
		t.Error("TruncationRecovery = false, want true")
	}

	// Debug defaults
	if cfg.DebugMode != "off" {
		t.Errorf("DebugMode = %q, want %q", cfg.DebugMode, "off")
	}
	if cfg.DebugDir != "debug_logs" {
		t.Errorf("DebugDir = %q, want %q", cfg.DebugDir, "debug_logs")
	}

	// Logging default
	if cfg.LogLevel != "INFO" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "INFO")
	}

	// Limits defaults
	if cfg.ToolDescriptionMaxLength != 10000 {
		t.Errorf("ToolDescriptionMaxLength = %d, want 10000", cfg.ToolDescriptionMaxLength)
	}
	if cfg.ModelCacheTTL != 3600*time.Second {
		t.Errorf("ModelCacheTTL = %v, want 3600s", cfg.ModelCacheTTL)
	}
	if cfg.DefaultMaxInputTokens != 200000 {
		t.Errorf("DefaultMaxInputTokens = %d, want 200000", cfg.DefaultMaxInputTokens)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// 2. Environment variables override defaults
// ---------------------------------------------------------------------------

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("SERVER_HOST", "127.0.0.1")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("KIRO_REGION", "eu-west-1")
	t.Setenv("FIRST_TOKEN_TIMEOUT", "30")
	t.Setenv("STREAMING_READ_TIMEOUT", "600")
	t.Setenv("DEBUG_MODE", "all")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("TOOL_DESCRIPTION_MAX_LENGTH", "5000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want %q", cfg.Host, "127.0.0.1")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9090)
	}
	if cfg.Region != "eu-west-1" {
		t.Errorf("Region = %q, want %q", cfg.Region, "eu-west-1")
	}
	if cfg.FirstTokenTimeout != 30*time.Second {
		t.Errorf("FirstTokenTimeout = %v, want 30s", cfg.FirstTokenTimeout)
	}
	if cfg.StreamingReadTimeout != 600*time.Second {
		t.Errorf("StreamingReadTimeout = %v, want 600s", cfg.StreamingReadTimeout)
	}
	if cfg.DebugMode != "all" {
		t.Errorf("DebugMode = %q, want %q", cfg.DebugMode, "all")
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "DEBUG")
	}
	if cfg.ToolDescriptionMaxLength != 5000 {
		t.Errorf("ToolDescriptionMaxLength = %d, want 5000", cfg.ToolDescriptionMaxLength)
	}
}

// ---------------------------------------------------------------------------
// 3. CLI flags override environment variables (applyCLIFlags)
// ---------------------------------------------------------------------------

func TestApplyCLIFlags_OverridesEnv(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	// Set env values that CLI should override.
	t.Setenv("SERVER_HOST", "10.0.0.1")
	t.Setenv("SERVER_PORT", "3000")

	origArgs := os.Args
	os.Args = []string{"gateway", "--host", "192.168.1.1", "--port", "4000"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q (CLI should override env)", cfg.Host, "192.168.1.1")
	}
	if cfg.Port != 4000 {
		t.Errorf("Port = %d, want %d (CLI should override env)", cfg.Port, 4000)
	}
}

func TestApplyCLIFlags_UnsetFlagsPreserveEnv(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	t.Setenv("SERVER_HOST", "10.0.0.1")
	t.Setenv("SERVER_PORT", "3000")

	origArgs := os.Args
	os.Args = []string{"gateway"} // no CLI flags
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Host != "10.0.0.1" {
		t.Errorf("Host = %q, want %q (env should be preserved)", cfg.Host, "10.0.0.1")
	}
	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want %d (env should be preserved)", cfg.Port, 3000)
	}
}

// ---------------------------------------------------------------------------
// 4. .env file loading
// ---------------------------------------------------------------------------

func TestLoad_DotEnvFileLoading(t *testing.T) {
	clearConfigEnvVars(t)

	// Create a temporary .env file in a temp directory and chdir there.
	tmpDir := t.TempDir()
	envContent := "REFRESH_TOKEN=env-file-token\nSERVER_PORT=7777\nKIRO_REGION=ap-southeast-1\n"
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("failed to write .env file: %v", err)
	}

	// Change to the temp directory so godotenv.Load() finds the .env file.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer os.Chdir(origDir)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.RefreshToken != "env-file-token" {
		t.Errorf("RefreshToken = %q, want %q (from .env file)", cfg.RefreshToken, "env-file-token")
	}
	if cfg.Port != 7777 {
		t.Errorf("Port = %d, want %d (from .env file)", cfg.Port, 7777)
	}
	if cfg.Region != "ap-southeast-1" {
		t.Errorf("Region = %q, want %q (from .env file)", cfg.Region, "ap-southeast-1")
	}
}

func TestLoad_RealEnvOverridesDotEnv(t *testing.T) {
	clearConfigEnvVars(t)

	// Create .env with one value, then set a real env var to override it.
	tmpDir := t.TempDir()
	envContent := "REFRESH_TOKEN=from-dotenv\nSERVER_PORT=1111\n"
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("failed to write .env file: %v", err)
	}

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Real env var should take precedence over .env file.
	t.Setenv("SERVER_PORT", "2222")

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Port != 2222 {
		t.Errorf("Port = %d, want %d (real env should override .env)", cfg.Port, 2222)
	}
}

// ---------------------------------------------------------------------------
// 5. Validation: missing credentials
// ---------------------------------------------------------------------------

func TestLoad_ValidationFailsWithNoCredentials(t *testing.T) {
	clearConfigEnvVars(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error when no credential source is configured")
	}

	// Error message should be actionable.
	errMsg := err.Error()
	if !contains(errMsg, "KIRO_CREDS_FILE") {
		t.Errorf("error should mention KIRO_CREDS_FILE, got: %s", errMsg)
	}
	if !contains(errMsg, "REFRESH_TOKEN") {
		t.Errorf("error should mention REFRESH_TOKEN, got: %s", errMsg)
	}
	if !contains(errMsg, "KIRO_CLI_DB_FILE") {
		t.Errorf("error should mention KIRO_CLI_DB_FILE, got: %s", errMsg)
	}
}

// ---------------------------------------------------------------------------
// 6. Validation passes with any single credential source
// ---------------------------------------------------------------------------

func TestLoad_ValidationPassesWithCredsFile(t *testing.T) {
	clearConfigEnvVars(t)
	t.Setenv("KIRO_CREDS_FILE", "/path/to/creds.json")

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should pass with KIRO_CREDS_FILE set, got: %v", err)
	}
	if cfg.CredsFile == "" {
		t.Error("CredsFile should not be empty")
	}
}

func TestLoad_ValidationPassesWithRefreshToken(t *testing.T) {
	clearConfigEnvVars(t)
	t.Setenv("REFRESH_TOKEN", "some-token")

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	_, err := Load()
	if err != nil {
		t.Fatalf("Load() should pass with REFRESH_TOKEN set, got: %v", err)
	}
}

func TestLoad_ValidationPassesWithCLIDBFile(t *testing.T) {
	clearConfigEnvVars(t)
	t.Setenv("KIRO_CLI_DB_FILE", "/path/to/data.sqlite3")

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	_, err := Load()
	if err != nil {
		t.Fatalf("Load() should pass with KIRO_CLI_DB_FILE set, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 7. Windows path normalization
// ---------------------------------------------------------------------------

func TestNormalizePath_EmptyString(t *testing.T) {
	result := normalizePath("")
	if result != "" {
		t.Errorf("normalizePath(\"\") = %q, want empty string", result)
	}
}

func TestNormalizePath_ForwardSlashes(t *testing.T) {
	result := normalizePath("/home/user/.aws/creds.json")
	want := filepath.Clean("/home/user/.aws/creds.json")
	if result != want {
		t.Errorf("normalizePath = %q, want %q", result, want)
	}
}

func TestNormalizePath_CleansPath(t *testing.T) {
	result := normalizePath("/home/user/../user/.aws/./creds.json")
	want := filepath.Clean("/home/user/.aws/creds.json")
	if result != want {
		t.Errorf("normalizePath = %q, want %q", result, want)
	}
}

// ---------------------------------------------------------------------------
// 8. envJSONMap: parse valid JSON, return default on invalid
// ---------------------------------------------------------------------------

func TestEnvJSONMap_ValidJSON(t *testing.T) {
	t.Setenv("TEST_JSON_MAP", `{"key1":"val1","key2":"val2"}`)
	result := envJSONMap("TEST_JSON_MAP", map[string]string{"default": "value"})
	if len(result) != 2 {
		t.Fatalf("envJSONMap returned %d entries, want 2", len(result))
	}
	if result["key1"] != "val1" {
		t.Errorf("result[key1] = %q, want %q", result["key1"], "val1")
	}
	if result["key2"] != "val2" {
		t.Errorf("result[key2] = %q, want %q", result["key2"], "val2")
	}
}

func TestEnvJSONMap_InvalidJSON(t *testing.T) {
	t.Setenv("TEST_JSON_MAP_BAD", `{not valid json}`)
	defaultVal := map[string]string{"fallback": "yes"}
	result := envJSONMap("TEST_JSON_MAP_BAD", defaultVal)
	if result["fallback"] != "yes" {
		t.Errorf("envJSONMap should return default on invalid JSON, got: %v", result)
	}
}

func TestEnvJSONMap_EmptyValue(t *testing.T) {
	t.Setenv("TEST_JSON_MAP_EMPTY", "")
	os.Unsetenv("TEST_JSON_MAP_EMPTY")
	defaultVal := map[string]string{"default": "val"}
	result := envJSONMap("TEST_JSON_MAP_EMPTY", defaultVal)
	if result["default"] != "val" {
		t.Errorf("envJSONMap should return default on empty value, got: %v", result)
	}
}

// ---------------------------------------------------------------------------
// 9. envCommaSeparated: split correctly, trim whitespace
// ---------------------------------------------------------------------------

func TestEnvCommaSeparated_BasicSplit(t *testing.T) {
	t.Setenv("TEST_CSV", "a,b,c")
	result := envCommaSeparated("TEST_CSV", nil)
	if len(result) != 3 || result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("envCommaSeparated = %v, want [a b c]", result)
	}
}

func TestEnvCommaSeparated_TrimsWhitespace(t *testing.T) {
	t.Setenv("TEST_CSV_WS", "  alpha , beta , gamma  ")
	result := envCommaSeparated("TEST_CSV_WS", nil)
	if len(result) != 3 || result[0] != "alpha" || result[1] != "beta" || result[2] != "gamma" {
		t.Errorf("envCommaSeparated = %v, want [alpha beta gamma]", result)
	}
}

func TestEnvCommaSeparated_EmptyReturnsDefault(t *testing.T) {
	t.Setenv("TEST_CSV_EMPTY", "")
	os.Unsetenv("TEST_CSV_EMPTY")
	defaultVal := []string{"x", "y"}
	result := envCommaSeparated("TEST_CSV_EMPTY", defaultVal)
	if len(result) != 2 || result[0] != "x" || result[1] != "y" {
		t.Errorf("envCommaSeparated should return default on empty, got: %v", result)
	}
}

func TestEnvCommaSeparated_OnlyCommasReturnsDefault(t *testing.T) {
	t.Setenv("TEST_CSV_COMMAS", ", , ,")
	defaultVal := []string{"fallback"}
	result := envCommaSeparated("TEST_CSV_COMMAS", defaultVal)
	if len(result) != 1 || result[0] != "fallback" {
		t.Errorf("envCommaSeparated should return default for only-commas input, got: %v", result)
	}
}

// ---------------------------------------------------------------------------
// 10. envBoolDefault: truthy/falsy values
// ---------------------------------------------------------------------------

func TestEnvBoolDefault_DefaultTrue(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		wantBool bool
	}{
		{"empty returns true", "", true},
		{"true returns true", "true", true},
		{"1 returns true", "1", true},
		{"yes returns true", "yes", true},
		{"random returns true", "anything", true},
		{"false returns false", "false", false},
		{"0 returns false", "0", false},
		{"no returns false", "no", false},
		{"disabled returns false", "disabled", false},
		{"off returns false", "off", false},
		{"FALSE returns false", "FALSE", false},
		{"Off returns false", "Off", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal == "" {
				os.Unsetenv("TEST_BOOL_DT")
			} else {
				t.Setenv("TEST_BOOL_DT", tt.envVal)
			}
			got := envBoolDefault("TEST_BOOL_DT", true)
			if got != tt.wantBool {
				t.Errorf("envBoolDefault(%q, true) = %v, want %v", tt.envVal, got, tt.wantBool)
			}
		})
	}
}

func TestEnvBoolDefault_DefaultFalse(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		wantBool bool
	}{
		{"empty returns false", "", false},
		{"random returns false", "anything", false},
		{"false returns false", "false", false},
		{"true returns true", "true", true},
		{"1 returns true", "1", true},
		{"yes returns true", "yes", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal == "" {
				os.Unsetenv("TEST_BOOL_DF")
			} else {
				t.Setenv("TEST_BOOL_DF", tt.envVal)
			}
			got := envBoolDefault("TEST_BOOL_DF", false)
			if got != tt.wantBool {
				t.Errorf("envBoolDefault(%q, false) = %v, want %v", tt.envVal, got, tt.wantBool)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. envEnum: returns default for invalid values
// ---------------------------------------------------------------------------

func TestEnvEnum_ValidValue(t *testing.T) {
	t.Setenv("TEST_ENUM", "errors")
	got := envEnum("TEST_ENUM", "off", []string{"off", "errors", "all"})
	if got != "errors" {
		t.Errorf("envEnum = %q, want %q", got, "errors")
	}
}

func TestEnvEnum_InvalidValue(t *testing.T) {
	t.Setenv("TEST_ENUM_BAD", "invalid_value")
	got := envEnum("TEST_ENUM_BAD", "off", []string{"off", "errors", "all"})
	if got != "off" {
		t.Errorf("envEnum should return default for invalid value, got %q", got)
	}
}

func TestEnvEnum_EmptyReturnsDefault(t *testing.T) {
	os.Unsetenv("TEST_ENUM_EMPTY")
	got := envEnum("TEST_ENUM_EMPTY", "off", []string{"off", "errors", "all"})
	if got != "off" {
		t.Errorf("envEnum should return default for empty, got %q", got)
	}
}

func TestEnvEnum_CaseInsensitive(t *testing.T) {
	t.Setenv("TEST_ENUM_CASE", "ALL")
	got := envEnum("TEST_ENUM_CASE", "off", []string{"off", "errors", "all"})
	if got != "all" {
		t.Errorf("envEnum should be case-insensitive, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 12. Timeout warning condition
// ---------------------------------------------------------------------------

func TestLoad_TimeoutWarningCondition(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	// Set FIRST_TOKEN_TIMEOUT >= STREAMING_READ_TIMEOUT to trigger warning.
	// The warning is logged (not an error), so Load() should still succeed.
	t.Setenv("FIRST_TOKEN_TIMEOUT", "300")
	t.Setenv("STREAMING_READ_TIMEOUT", "300")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed even with timeout warning, got: %v", err)
	}

	if cfg.FirstTokenTimeout != 300*time.Second {
		t.Errorf("FirstTokenTimeout = %v, want 300s", cfg.FirstTokenTimeout)
	}
	if cfg.StreamingReadTimeout != 300*time.Second {
		t.Errorf("StreamingReadTimeout = %v, want 300s", cfg.StreamingReadTimeout)
	}
}

func TestLoad_NoWarningWhenTimeoutsCorrect(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("FIRST_TOKEN_TIMEOUT", "15")
	t.Setenv("STREAMING_READ_TIMEOUT", "300")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.FirstTokenTimeout >= cfg.StreamingReadTimeout {
		t.Error("FirstTokenTimeout should be less than StreamingReadTimeout")
	}
}

// ---------------------------------------------------------------------------
// envStr, envInt, envFloat helpers
// ---------------------------------------------------------------------------

func TestEnvStr_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_STR", "hello")
	got := envStr("TEST_STR", "default")
	if got != "hello" {
		t.Errorf("envStr = %q, want %q", got, "hello")
	}
}

func TestEnvStr_ReturnsDefault(t *testing.T) {
	os.Unsetenv("TEST_STR_MISSING")
	got := envStr("TEST_STR_MISSING", "default")
	if got != "default" {
		t.Errorf("envStr = %q, want %q", got, "default")
	}
}

func TestEnvInt_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	got := envInt("TEST_INT", 0)
	if got != 42 {
		t.Errorf("envInt = %d, want %d", got, 42)
	}
}

func TestEnvInt_ReturnsDefaultOnInvalid(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "not-a-number")
	got := envInt("TEST_INT_BAD", 99)
	if got != 99 {
		t.Errorf("envInt should return default on invalid, got %d", got)
	}
}

func TestEnvInt_ReturnsDefaultOnEmpty(t *testing.T) {
	os.Unsetenv("TEST_INT_EMPTY")
	got := envInt("TEST_INT_EMPTY", 99)
	if got != 99 {
		t.Errorf("envInt should return default on empty, got %d", got)
	}
}

func TestEnvFloat_ReturnsEnvValue(t *testing.T) {
	t.Setenv("TEST_FLOAT", "3.14")
	got := envFloat("TEST_FLOAT", 0)
	if got != 3.14 {
		t.Errorf("envFloat = %f, want %f", got, 3.14)
	}
}

func TestEnvFloat_ReturnsDefaultOnInvalid(t *testing.T) {
	t.Setenv("TEST_FLOAT_BAD", "abc")
	got := envFloat("TEST_FLOAT_BAD", 1.5)
	if got != 1.5 {
		t.Errorf("envFloat should return default on invalid, got %f", got)
	}
}

// ---------------------------------------------------------------------------
// Complex config values in Load()
// ---------------------------------------------------------------------------

func TestLoad_HiddenModelsFromJSON(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("HIDDEN_MODELS", `{"model-a":"INTERNAL_A","model-b":"INTERNAL_B"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if len(cfg.HiddenModels) != 2 {
		t.Fatalf("HiddenModels len = %d, want 2", len(cfg.HiddenModels))
	}
	if cfg.HiddenModels["model-a"] != "INTERNAL_A" {
		t.Errorf("HiddenModels[model-a] = %q, want %q", cfg.HiddenModels["model-a"], "INTERNAL_A")
	}
}

func TestLoad_ModelAliasesFromJSON(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("MODEL_ALIASES", `{"my-model":"real-model-id"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.ModelAliases["my-model"] != "real-model-id" {
		t.Errorf("ModelAliases[my-model] = %q, want %q", cfg.ModelAliases["my-model"], "real-model-id")
	}
}

func TestLoad_HiddenFromListCommaSeparated(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("HIDDEN_FROM_LIST", "auto, internal-model, test-model")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if len(cfg.HiddenFromList) != 3 {
		t.Fatalf("HiddenFromList len = %d, want 3", len(cfg.HiddenFromList))
	}
	if cfg.HiddenFromList[0] != "auto" {
		t.Errorf("HiddenFromList[0] = %q, want %q", cfg.HiddenFromList[0], "auto")
	}
	if cfg.HiddenFromList[1] != "internal-model" {
		t.Errorf("HiddenFromList[1] = %q, want %q", cfg.HiddenFromList[1], "internal-model")
	}
}

func TestLoad_FakeReasoningOpenTagsCommaSeparated(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	t.Setenv("FAKE_REASONING_OPEN_TAGS", "<custom1>, <custom2>")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if len(cfg.FakeReasoningOpenTags) != 2 {
		t.Fatalf("FakeReasoningOpenTags len = %d, want 2", len(cfg.FakeReasoningOpenTags))
	}
	if cfg.FakeReasoningOpenTags[0] != "<custom1>" {
		t.Errorf("FakeReasoningOpenTags[0] = %q, want %q", cfg.FakeReasoningOpenTags[0], "<custom1>")
	}
}

// ---------------------------------------------------------------------------
// Fallback models
// ---------------------------------------------------------------------------

func TestLoad_FallbackModels(t *testing.T) {
	clearConfigEnvVars(t)
	setCredentialSource(t)

	origArgs := os.Args
	os.Args = []string{"gateway"}
	defer func() { os.Args = origArgs }()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if len(cfg.FallbackModels) == 0 {
		t.Fatal("FallbackModels should not be empty")
	}
	// Check that "auto" is the first fallback model.
	if cfg.FallbackModels[0].ModelID != "auto" {
		t.Errorf("FallbackModels[0].ModelID = %q, want %q", cfg.FallbackModels[0].ModelID, "auto")
	}
}

// ---------------------------------------------------------------------------
// Validate function directly
// ---------------------------------------------------------------------------

func TestValidate_AllCredentialSources(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantError bool
	}{
		{
			name:      "no credentials",
			cfg:       Config{},
			wantError: true,
		},
		{
			name:      "only CredsFile",
			cfg:       Config{CredsFile: "/path/to/file"},
			wantError: false,
		},
		{
			name:      "only RefreshToken",
			cfg:       Config{RefreshToken: "token"},
			wantError: false,
		},
		{
			name:      "only CLIDBFile",
			cfg:       Config{CLIDBFile: "/path/to/db"},
			wantError: false,
		},
		{
			name:      "all credentials set",
			cfg:       Config{CredsFile: "/f", RefreshToken: "t", CLIDBFile: "/d"},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(&tt.cfg)
			if tt.wantError && err == nil {
				t.Error("validate() should return error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("validate() returned unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
