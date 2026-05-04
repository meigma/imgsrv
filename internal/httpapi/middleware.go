package httpapi

import "net/http"

// Middleware wraps an HTTP handler with cross-cutting behavior.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware in the order provided.
func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}
