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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
	"github.com/jwadow/kiro-gateway/gateway/internal/client"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/debug"
	"github.com/jwadow/kiro-gateway/gateway/internal/logging"
	"github.com/jwadow/kiro-gateway/gateway/internal/models"
	"github.com/jwadow/kiro-gateway/gateway/internal/resolver"
	"github.com/jwadow/kiro-gateway/gateway/internal/server"
	"github.com/jwadow/kiro-gateway/gateway/internal/truncation"
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
// fallback model list from config.
func loadModelsAtStartup(cfg *config.Config, authMgr auth.AuthManager, modelCache cache.ModelCache) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	modelList, err := fetchModelsFromKiro(ctx, cfg, authMgr)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load models from Kiro API, using fallback list")
		fallback := make([]models.ModelInfo, 0, len(cfg.FallbackModels))
		for _, fm := range cfg.FallbackModels {
			fallback = append(fallback, models.ModelInfo{
				ModelID:        fm.ModelID,
				MaxInputTokens: cfg.DefaultMaxInputTokens,
				DisplayName:    fm.ModelID,
			})
		}
		modelCache.Update(fallback)
		log.Info().Int("count", len(fallback)).Msg("Loaded fallback models")
		return
	}

	modelCache.Update(modelList)
	log.Info().Int("count", len(modelList)).Msg("Loaded models from Kiro API")
}

// fetchModelsFromKiro calls the Kiro ListAvailableModels API and returns
// the parsed model list. This is a best-effort call at startup.
func fetchModelsFromKiro(ctx context.Context, cfg *config.Config, authMgr auth.AuthManager) ([]models.ModelInfo, error) {
	token, err := authMgr.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	url := fmt.Sprintf("%s/ListAvailableModels", authMgr.QHost())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
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

// printBanner prints the startup banner with server URL and useful paths.
func printBanner(cfg *config.Config) {
	addr := fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)
	if cfg.Host == "0.0.0.0" {
		addr = fmt.Sprintf("http://localhost:%d", cfg.Port)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║                  👻 Kiro Gateway                    ║")
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

	// Start the server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

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
