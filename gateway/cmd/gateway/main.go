// Kiro Gateway - Go implementation
//
// Entry point for the gateway binary. Handles configuration loading,
// dependency injection wiring, startup model loading, and graceful
// lifecycle management (SIGINT/SIGTERM).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/auth"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/cache"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/client"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/config"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/debug"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/logging"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/models"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/resolver"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/server"
	"github.com/chasedputnam/go-kiro-gateway/gateway/internal/truncation"
)

// version is set at compile time via ldflags:
//
//	go build -ldflags "-X main.version=1.0.0" ./cmd/gateway
var version = "dev"

// shutdownTimeout is the maximum time to wait for in-flight requests
// to complete during graceful shutdown.
const shutdownTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run contains the full application lifecycle. It returns an error on
// fatal startup failures; graceful shutdown returns nil.
func run() error {
	// 1. Load configuration.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cfg.Version = version

	// 2. Initialize structured logging.
	logging.Init(cfg.LogLevel, nil)

	log.Info().Str("version", version).Msg("Kiro Gateway starting")

	// 3. Initialize auth manager.
	authMgr, err := auth.NewAuthManager(cfg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// 4. Initialize model cache and load models from Kiro API.
	modelCache := cache.New(cfg.ModelCacheTTL)
	loadModelsAtStartup(cfg, authMgr, modelCache)

	// Add hidden models to cache.
	for displayName, internalID := range cfg.HiddenModels {
		modelCache.AddHiddenModel(displayName, internalID)
	}

	// 5. Initialize model resolver.
	modelResolver := resolver.New(modelCache, resolver.Config{
		HiddenModels:   cfg.HiddenModels,
		Aliases:        cfg.ModelAliases,
		HiddenFromList: cfg.HiddenFromList,
	})

	// 6. Initialize HTTP client.
	kiroClient := client.NewKiroClient(authMgr, cfg)

	// 7. Initialize debug logger.
	debugLogger := debug.NewDebugLogger(cfg.DebugMode, cfg.DebugDir)

	// 4b. Log the startup model fetch for debug visibility.
	logStartupModels(debugLogger, modelCache)

	// 8. Initialize truncation state.
	truncState := truncation.NewState()

	// 9. Create server with all dependencies.
	srv := server.New(cfg, authMgr, modelCache, modelResolver, kiroClient, debugLogger, truncState)

	// 10. Print startup banner.
	printBanner(cfg)

	// 11. Start server with graceful shutdown.
	return startWithGracefulShutdown(srv, cfg)
}

// loadModelsAtStartup attempts to load models from the Kiro
// ListAvailableModels API. On failure it falls back to the hardcoded
// fallback model list from config, logging enough detail for operators
// to diagnose the failure.
func loadModelsAtStartup(cfg *config.Config, authMgr auth.AuthManager, modelCache cache.ModelCache) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	log.Info().Str("host", authMgr.QHost()).Msg("Fetching models from Kiro API")

	modelList, err := fetchModelsFromKiro(ctx, cfg, authMgr)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load models from Kiro API, using fallback list")

		fallbackIDs := make([]string, 0, len(cfg.FallbackModels))
		fallback := make([]models.ModelInfo, 0, len(cfg.FallbackModels))
		for _, fm := range cfg.FallbackModels {
			fallback = append(fallback, models.ModelInfo{
				ModelID:        fm.ModelID,
				MaxInputTokens: cfg.DefaultMaxInputTokens,
				DisplayName:    fm.ModelID,
			})
			fallbackIDs = append(fallbackIDs, fm.ModelID)
		}
		modelCache.Update(fallback)

		log.Info().
			Int("count", len(fallback)).
			Strs("models", fallbackIDs).
			Msg("Loaded fallback models")
		return
	}

	loadedIDs := make([]string, 0, len(modelList))
	for _, m := range modelList {
		loadedIDs = append(loadedIDs, m.ModelID)
	}
	modelCache.Update(modelList)

	log.Info().
		Int("count", len(modelList)).
		Strs("models", loadedIDs).
		Msg("Loaded models from Kiro API")
}

// fetchModelsFromKiro calls the Kiro ListAvailableModels API and returns
// the parsed model list. This is a best-effort call at startup. Each step
// is logged so operators can pinpoint failures from the console output.
func fetchModelsFromKiro(ctx context.Context, cfg *config.Config, authMgr auth.AuthManager) ([]models.ModelInfo, error) {
	token, err := authMgr.GetAccessToken(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to obtain access token for model fetch")
		return nil, fmt.Errorf("get access token: %w", err)
	}
	log.Debug().Msg("Access token obtained for model fetch")

	apiURL := fmt.Sprintf("%s/ListAvailableModels", authMgr.QHost())
	log.Debug().Str("url", apiURL).Msg("Requesting model list from Kiro API")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		log.Error().Err(err).Str("url", apiURL).Msg("Failed to create model list request")
		return nil, fmt.Errorf("create request: %w", err)
	}
	client.SetKiroHeaders(req, token, authMgr.Fingerprint(), authMgr.ProfileARN())

	// The Kiro API requires profileArn and origin on every request.
	// For GET requests these are sent as query parameters.
	profileARN := authMgr.ProfileARN()
	q := req.URL.Query()
	q.Set("profileArn", profileARN)
	q.Set("origin", "AI_EDITOR")
	req.URL.RawQuery = q.Encode()

	log.Debug().
		Str("url", req.URL.String()).
		Str("profileArn", profileARN).
		Msg("Sending ListAvailableModels request")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error().Err(err).Str("url", apiURL).Msg("HTTP request to Kiro API failed")
		return nil, fmt.Errorf("HTTP request to %s: %w", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the response body for diagnostic detail.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Error().
			Int("status", resp.StatusCode).
			Str("url", apiURL).
			Str("body", string(errBody)).
			Msg("Kiro API returned non-200 status for model list")
		return nil, fmt.Errorf("ListAvailableModels returned status %d: %s", resp.StatusCode, string(errBody))
	}

	// Parse the response. The Kiro API returns a JSON object with a
	// "models" array. Each entry has at least "modelId".
	type kiroModel struct {
		ModelID        string `json:"modelId"`
		MaxInputTokens int    `json:"maxInputTokens"`
	}
	type listModelsResponse struct {
		Models []kiroModel `json:"models"`
	}

	var body listModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Error().Err(err).Str("url", apiURL).Msg("Failed to decode model list response")
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := make([]models.ModelInfo, 0, len(body.Models))
	for _, m := range body.Models {
		maxTokens := m.MaxInputTokens
		if maxTokens <= 0 {
			maxTokens = cfg.DefaultMaxInputTokens
		}
		result = append(result, models.ModelInfo{
			ModelID:        m.ModelID,
			MaxInputTokens: maxTokens,
			DisplayName:    m.ModelID,
		})
	}

	return result, nil
}

// logStartupModels writes the loaded model list to the debug log directory
// so operators can verify which models were discovered at startup. This runs
// after the debug logger is initialised and the model cache is populated.
func logStartupModels(dl debug.DebugLogger, modelCache cache.ModelCache) {
	modelIDs := modelCache.GetAllModelIDs()
	if len(modelIDs) == 0 {
		return
	}

	info := map[string]any{
		"event":     "startup_models_loaded",
		"model_ids": modelIDs,
		"count":     len(modelIDs),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	dl.LogAppMessage(fmt.Sprintf("[startup] Loaded %d models: %s", len(modelIDs), string(data)))
}

// printBanner prints the startup banner with server URL and useful paths.
func printBanner(cfg *config.Config) {
	addr := fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)
	if cfg.Host == "0.0.0.0" {
		addr = fmt.Sprintf("http://localhost:%d", cfg.Port)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║                  Go Kiro Gateway                     ║")
	fmt.Printf("║  Version: %-42s ║\n", cfg.Version)
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Server:  %-42s ║\n", addr)
	fmt.Printf("║  Health:  %-42s ║\n", addr+"/health")
	fmt.Printf("║  Models:  %-42s ║\n", addr+"/v1/models")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
}

// startWithGracefulShutdown starts the HTTP server and handles SIGINT/SIGTERM
// for graceful shutdown. It waits for in-flight requests to complete within
// the shutdown timeout.
func startWithGracefulShutdown(srv *server.Server, cfg *config.Config) error {
	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	// Start the server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Give the server a moment to bind, then verify it's reachable.
	// This catches "address already in use" and similar errors early.
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server failed to start on %s: %w", addr, err)
		}
	case <-time.After(250 * time.Millisecond):
		// Server goroutine hasn't errored — verify with a health check.
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", cfg.Port)
		healthResp, err := http.Get(healthURL)
		if err != nil {
			log.Warn().Err(err).Str("url", healthURL).Msg("Server started but health check failed — verify the listening address")
		} else {
			healthResp.Body.Close()
			log.Info().
				Str("addr", addr).
				Int("port", cfg.Port).
				Str("anthropic_base_url", fmt.Sprintf("http://localhost:%d", cfg.Port)).
				Str("openai_base_url", fmt.Sprintf("http://localhost:%d/v1", cfg.Port)).
				Msg("Server listening and healthy")
		}
	}

	// Wait for shutdown signal or server error.
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
	case <-ctx.Done():
		log.Info().Msg("Shutdown signal received, draining connections...")

		if err := srv.Shutdown(shutdownTimeout); err != nil {
			log.Error().Err(err).Msg("Shutdown error")
			return fmt.Errorf("shutdown: %w", err)
		}

		log.Info().Msg("Server stopped gracefully")
	}

	return nil
}
