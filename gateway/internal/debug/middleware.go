package debug

import (
	"bytes"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"
)

// loggedEndpoints are the API paths that should have debug logging enabled.
// Health checks, documentation, and other endpoints are not logged.
var loggedEndpoints = map[string]bool{
	"/v1/chat/completions": true,
	"/v1/messages":         true,
}

// Middleware returns a chi-compatible middleware that initialises debug
// logging before the handler runs. It captures the raw request body and
// wraps the response writer to capture response data.
//
// The middleware only activates for API endpoints defined in loggedEndpoints.
// Flush/discard operations are handled by route handlers and error handlers.
func Middleware(dl DebugLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip non-API endpoints.
			if !loggedEndpoints[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Initialise debug logging for this request.
			dl.PrepareNewRequest()

			// Read and log the raw request body, then restore it so
			// downstream handlers can read it again.
			body, err := io.ReadAll(r.Body)
			if err != nil {
				log.Warn().Err(err).Msg("failed to read request body for debug logging")
			} else if len(body) > 0 {
				dl.LogRequestBody(body)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			next.ServeHTTP(w, r)
		})
	}
}
