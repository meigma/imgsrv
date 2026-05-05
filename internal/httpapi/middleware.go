package httpapi

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/felixge/httpsnoop"
)

// Middleware wraps an HTTP handler with cross-cutting behavior.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware in the order provided.
func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

// logRequests returns middleware that emits one structured log entry per request.
//
// The log level is selected by requestLogLevel based on the response status code.
// A nil logger is replaced with a discarding [slog.Logger] so callers may omit it.
func logRequests(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metrics := httpsnoop.CaptureMetrics(next, w, r)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", metrics.Code),
				slog.Int64("bytes", metrics.Written),
				slog.Duration("duration", metrics.Duration),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			}
			if r.Pattern != "" {
				attrs = append(attrs, slog.String("route", r.Pattern))
			}

			logger.LogAttrs(r.Context(), requestLogLevel(metrics.Code), "http request", attrs...)
		})
	}
}

// requestLogLevel maps an HTTP response status code to a slog level.
func requestLogLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
