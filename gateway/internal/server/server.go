// Package server wires the chi router, registers middleware, mounts route
// handlers, and manages the HTTP server lifecycle for Kiro Gateway.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jwadow/kiro-gateway/gateway/internal/auth"
	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
	"github.com/jwadow/kiro-gateway/gateway/internal/client"
	"github.com/jwadow/kiro-gateway/gateway/internal/config"
	"github.com/jwadow/kiro-gateway/gateway/internal/debug"
	"github.com/jwadow/kiro-gateway/gateway/internal/middleware"
	"github.com/jwadow/kiro-gateway/gateway/internal/resolver"
	"github.com/jwadow/kiro-gateway/gateway/internal/truncation"
)

// Server holds all dependencies and the chi router for the gateway.
type Server struct {
	config      *config.Config
	router      chi.Router
	auth        auth.AuthManager
	cache       cache.ModelCache
	resolver    resolver.Resolver
	httpClient  client.KiroClient
	debugLogger debug.DebugLogger
	truncState  *truncation.State

	mu         sync.Mutex
	httpServer *http.Server
}

// New creates a Server with all dependencies injected, wires the chi
// router with the middleware stack (CORS → Auth → Debug), and registers
// all route groups.
func New(
	cfg *config.Config,
	authMgr auth.AuthManager,
	modelCache cache.ModelCache,
	modelResolver resolver.Resolver,
	kiroClient client.KiroClient,
	debugLogger debug.DebugLogger,
	truncState *truncation.State,
) *Server {
	s := &Server{
		config:      cfg,
		auth:        authMgr,
		cache:       modelCache,
		resolver:    modelResolver,
		httpClient:  kiroClient,
		debugLogger: debugLogger,
		truncState:  truncState,
	}

	s.router = s.buildRouter()
	return s
}

// Router returns the underlying chi.Router. Useful for testing.
func (s *Server) Router() chi.Router {
	return s.router
}

// Start creates and starts the HTTP server. It blocks until the server
// is shut down. Callers should use Shutdown() for graceful termination.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.mu.Lock()
	s.httpServer = srv
	s.mu.Unlock()

	return srv.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server, waiting for in-flight
// requests to complete within the given timeout.
func (s *Server) Shutdown(timeout time.Duration) error {
	s.mu.Lock()
	srv := s.httpServer
	s.mu.Unlock()

	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return srv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// Router construction
// ---------------------------------------------------------------------------

// buildRouter creates the chi router with middleware and routes.
func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	// Global middleware stack: CORS → Auth → Debug.
	r.Use(middleware.CORS())
	r.Use(middleware.Auth(s.config.ProxyAPIKey))
	r.Use(debug.Middleware(s.debugLogger))

	// Health routes — these pass through the auth middleware without
	// validation because the Auth middleware skips paths not in its
	// protectedRoutes map.
	r.Get("/", s.handleRoot)
	r.Get("/health", s.handleHealth)

	// OpenAI-compatible API routes.
	r.Get("/v1/models", s.handleListModels)
	r.Post("/v1/chat/completions", s.handleChatCompletions)

	// Anthropic-compatible API routes.
	r.Post("/v1/messages", s.handleMessages)

	return r
}

// ---------------------------------------------------------------------------
// Health route handlers
// ---------------------------------------------------------------------------

// handleRoot serves GET / with a JSON status response.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]string{
		"status":  "ok",
		"message": s.config.Description,
		"version": s.config.Version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleHealth serves GET /health with a JSON health check response
// including a timestamp.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   s.config.Version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// writeJSON marshals v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
