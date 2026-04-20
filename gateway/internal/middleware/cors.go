// Package middleware provides HTTP middleware for Kiro Gateway.
//
// This package contains CORS and API key validation middleware that are
// applied to the chi router before route handlers execute.
package middleware

import (
	"net/http"

	"github.com/go-chi/cors"
)

// CORS returns a chi-compatible middleware that applies permissive CORS
// headers to all responses. The gateway allows all origins, all methods,
// and all headers so that any client can connect without restrictions.
func CORS() func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"*"},
		AllowCredentials: false,
		MaxAge:           86400, // 24 hours
	})
}
