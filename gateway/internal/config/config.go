// Package config provides centralized configuration loading for Kiro Gateway.
//
// Configuration is loaded from three sources with the following priority
// (highest to lowest): CLI flags → environment variables → default values.
// A .env file in the working directory is loaded first via godotenv, so
// environment variables set there act as defaults that real env vars override.
package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all gateway settings. Fields are populated by Load().
type Config struct {
	// Server
	Host string
	Port int

	// Auth
	ProxyAPIKey  string
	RefreshToken string
	ProfileARN   string
	Region       string
	CredsFile    string
	CLIDBFile    string

	// Proxy
	VPNProxyURL string

	// Timeouts
	FirstTokenTimeout    time.Duration
	FirstTokenMaxRetries int
	StreamingReadTimeout time.Duration

	// Fake Reasoning
	FakeReasoningEnabled       bool
	FakeReasoningMaxTokens     int
	FakeReasoningHandling      string
	FakeReasoningInitialBuffer int
	FakeReasoningOpenTags      []string

	// Truncation
	TruncationRecovery bool

	// Debug
	DebugMode string
	DebugDir  string

	// Logging
	LogLevel string

	// Models
	HiddenModels   map[string]string
	ModelAliases   map[string]string
	HiddenFromList []string
	FallbackModels []FallbackModel

	// Limits
	ToolDescriptionMaxLength int
	ModelCacheTTL            time.Duration
	DefaultMaxInputTokens    int
	MaxRetries               int
	BaseRetryDelay           time.Duration
	TokenRefreshThreshold    time.Duration

	// App
	Version     string
	Title       string
	Description string
}

// FallbackModel represents a model entry used when the ListAvailableModels
// API is unreachable.
type FallbackModel struct {
	ModelID string `json:"modelId"`
}

// Load reads configuration from .env file, environment variables, and CLI
// flags. It validates required fields and returns an actionable error when
// the configuration is invalid.
func Load() (*Config, error) {
	// 1. Load .env file (errors are non-fatal — file may not exist).
	_ = godotenv.Load()

	cfg := &Config{}

	// 2. Populate from environment variables with defaults.
	cfg.Host = envStr("SERVER_HOST", "0.0.0.0")
	cfg.Port = envInt("SERVER_PORT", 8000)

	cfg.ProxyAPIKey = envStr("PROXY_API_KEY", "my-super-secret-password-123")
	cfg.RefreshToken = envStr("REFRESH_TOKEN", "")
	cfg.ProfileARN = envStr("PROFILE_ARN", "")
	cfg.Region = envStr("KIRO_REGION", "us-east-1")
	cfg.CredsFile = normalizePath(envStr("KIRO_CREDS_FILE", ""))
	cfg.CLIDBFile = normalizePath(envStr("KIRO_CLI_DB_FILE", ""))

	cfg.VPNProxyURL = envStr("VPN_PROXY_URL", "")

	cfg.FirstTokenTimeout = time.Duration(envFloat("FIRST_TOKEN_TIMEOUT", 15)) * time.Second
	cfg.FirstTokenMaxRetries = envInt("FIRST_TOKEN_MAX_RETRIES", 3)
	cfg.StreamingReadTimeout = time.Duration(envFloat("STREAMING_READ_TIMEOUT", 300)) * time.Second

	cfg.FakeReasoningEnabled = envBoolDefault("FAKE_REASONING", true)
	cfg.FakeReasoningMaxTokens = envInt("FAKE_REASONING_MAX_TOKENS", 4000)
	cfg.FakeReasoningHandling = envEnum("FAKE_REASONING_HANDLING", "as_reasoning_content",
		[]string{"as_reasoning_content", "remove", "pass", "strip_tags"})
	cfg.FakeReasoningInitialBuffer = envInt("FAKE_REASONING_INITIAL_BUFFER_SIZE", 20)
	cfg.FakeReasoningOpenTags = envCommaSeparated("FAKE_REASONING_OPEN_TAGS",
		[]string{"<thinking>", "<think>", "<reasoning>", "<thought>"})

	cfg.TruncationRecovery = envBoolDefault("TRUNCATION_RECOVERY", true)

	cfg.DebugMode = envEnum("DEBUG_MODE", "off", []string{"off", "errors", "all"})
	cfg.DebugDir = envStr("DEBUG_DIR", "debug_logs")

	cfg.LogLevel = strings.ToUpper(envStr("LOG_LEVEL", "INFO"))

	cfg.HiddenModels = envJSONMap("HIDDEN_MODELS", map[string]string{
		"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	})
	cfg.ModelAliases = envJSONMap("MODEL_ALIASES", map[string]string{
		"auto-kiro": "auto",
	})
	cfg.HiddenFromList = envCommaSeparated("HIDDEN_FROM_LIST", []string{"auto"})
	cfg.FallbackModels = defaultFallbackModels()

	cfg.ToolDescriptionMaxLength = envInt("TOOL_DESCRIPTION_MAX_LENGTH", 10000)
	cfg.ModelCacheTTL = time.Duration(envInt("MODEL_CACHE_TTL", 3600)) * time.Second
	cfg.DefaultMaxInputTokens = envInt("DEFAULT_MAX_INPUT_TOKENS", 200000)
	cfg.MaxRetries = envInt("MAX_RETRIES", 3)
	cfg.BaseRetryDelay = time.Duration(envFloat("BASE_RETRY_DELAY", 1.0)*1000) * time.Millisecond
	cfg.TokenRefreshThreshold = time.Duration(envInt("TOKEN_REFRESH_THRESHOLD", 600)) * time.Second

	cfg.Version = "0.0.3"
	cfg.Title = "Go Kiro Gateway"
	cfg.Description = "Proxy gateway for Kiro API (Amazon Q Developer / AWS CodeWhisperer). OpenAI and Anthropic compatible."

	// 3. CLI flags override environment variables.
	applyCLIFlags(cfg)

	// 4. Warnings.
	if cfg.FirstTokenTimeout >= cfg.StreamingReadTimeout {
		warnTimeoutConfig(cfg.FirstTokenTimeout, cfg.StreamingReadTimeout)
	}

	// 5. Validate required fields.
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ---------------------------------------------------------------------------
// CLI flag parsing
// ---------------------------------------------------------------------------

// applyCLIFlags parses --host and --port flags and applies them with highest
// priority. Flags that are not explicitly set on the command line are ignored
// so that environment/default values are preserved.
func applyCLIFlags(cfg *Config) {
	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)

	host := fs.String("host", "", "Server host address")
	port := fs.Int("port", 0, "Server port number")

	// Parse os.Args[1:]; ignore errors from unknown flags so that test
	// binaries with their own flags don't break startup.
	_ = fs.Parse(os.Args[1:])

	if *host != "" {
		cfg.Host = *host
	}
	if *port != 0 {
		cfg.Port = *port
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// validate checks that the configuration has at least one credential source.
func validate(cfg *Config) error {
	hasCredsFile := cfg.CredsFile != ""
	hasRefreshToken := cfg.RefreshToken != ""
	hasCLIDB := cfg.CLIDBFile != ""

	if !hasCredsFile && !hasRefreshToken && !hasCLIDB {
		return fmt.Errorf(
			"no credential source configured. Please set at least one of:\n" +
				"  • KIRO_CREDS_FILE  — path to JSON credentials file from Kiro IDE\n" +
				"  • REFRESH_TOKEN    — Kiro refresh token\n" +
				"  • KIRO_CLI_DB_FILE — path to kiro-cli SQLite database\n\n" +
				"See .env.example for detailed instructions")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Timeout warning
// ---------------------------------------------------------------------------

func warnTimeoutConfig(firstToken, streamingRead time.Duration) {
	log.Printf(
		"\n⚠️  WARNING: Suboptimal timeout configuration detected.\n\n"+
			"    FIRST_TOKEN_TIMEOUT (%.0fs) >= STREAMING_READ_TIMEOUT (%.0fs)\n\n"+
			"    These timeouts serve different purposes:\n"+
			"      - FIRST_TOKEN_TIMEOUT: time to wait for model to START responding (default: 15s)\n"+
			"      - STREAMING_READ_TIMEOUT: time to wait BETWEEN chunks during streaming (default: 300s)\n\n"+
			"    Recommendation: FIRST_TOKEN_TIMEOUT should be LESS than STREAMING_READ_TIMEOUT.\n\n"+
			"    Example configuration:\n"+
			"      FIRST_TOKEN_TIMEOUT=15\n"+
			"      STREAMING_READ_TIMEOUT=300\n",
		firstToken.Seconds(), streamingRead.Seconds(),
	)
}

// ---------------------------------------------------------------------------
// Environment variable helpers
// ---------------------------------------------------------------------------

// envStr returns the environment variable value or the provided default.
func envStr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envInt returns the environment variable parsed as int, or the default.
func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// envFloat returns the environment variable parsed as float64, or the default.
func envFloat(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// envBoolDefault parses a boolean env var where the default is the given value.
// When defaultTrue is true, the variable must be explicitly set to a falsy
// value ("false", "0", "no", "disabled", "off") to disable the feature.
// When defaultTrue is false, the variable must be explicitly set to a truthy
// value ("true", "1", "yes") to enable the feature.
func envBoolDefault(key string, defaultTrue bool) bool {
	v := strings.ToLower(os.Getenv(key))
	if v == "" {
		return defaultTrue
	}
	if defaultTrue {
		// Default is true — only explicit falsy values disable it.
		return v != "false" && v != "0" && v != "no" && v != "disabled" && v != "off"
	}
	// Default is false — only explicit truthy values enable it.
	return v == "true" || v == "1" || v == "yes"
}

// envEnum returns the env var value if it matches one of the allowed values,
// otherwise returns the default.
func envEnum(key, defaultVal string, allowed []string) string {
	v := strings.ToLower(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return defaultVal
}

// envJSONMap parses a JSON object from an environment variable into a
// map[string]string. Returns the default map on parse failure or empty value.
func envJSONMap(key string, defaultVal map[string]string) map[string]string {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return defaultVal
	}
	return m
}

// envCommaSeparated splits a comma-separated env var into a string slice.
// Each element is trimmed of whitespace. Returns the default on empty value.
func envCommaSeparated(key string, defaultVal []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return defaultVal
	}
	return result
}

// ---------------------------------------------------------------------------
// Path normalization
// ---------------------------------------------------------------------------

// normalizePath converts backslashes to forward slashes on Windows and cleans
// the path. Returns empty string for empty input.
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	// On Windows, convert backslashes to the OS-native separator, then
	// use filepath.Clean to normalize. On Unix this is a no-op for forward
	// slashes.
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "\\", string(filepath.Separator))
	}
	return filepath.Clean(p)
}

// ---------------------------------------------------------------------------
// Default fallback models
// ---------------------------------------------------------------------------

func defaultFallbackModels() []FallbackModel {
	return []FallbackModel{
		{ModelID: "auto"},
		{ModelID: "claude-opus-4.6"},
		{ModelID: "claude-opus-4.5"},
		{ModelID: "claude-sonnet-4.6"},
		{ModelID: "claude-sonnet-4.5"},
		{ModelID: "claude-haiku-4.5"},
	}
}
