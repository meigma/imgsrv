package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
)

// Dependencies contains adapters used by the HTTP API.
type Dependencies struct {
	// Logger receives HTTP adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger

	// Readiness reports whether the service can accept operational traffic.
	Readiness ReadinessChecker
}

// ReadinessChecker reports whether the service can accept operational traffic.
type ReadinessChecker interface {
	CheckReady(context.Context) error
}

// ReadinessFunc adapts a function into a ReadinessChecker.
type ReadinessFunc func(context.Context) error

// CheckReady calls f(ctx).
func (f ReadinessFunc) CheckReady(ctx context.Context) error {
	return f(ctx)
}

type api struct {
	logger    *slog.Logger
	readiness ReadinessChecker
}

// New constructs the HTTP API handler.
func New(deps Dependencies) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	readiness := deps.Readiness
	if readiness == nil {
		readiness = ReadinessFunc(func(context.Context) error { return nil })
	}

	api := &api{
		logger:    logger,
		readiness: readiness,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.healthz)
	mux.HandleFunc("GET /readyz", api.readyz)

	return Chain(mux)
}

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	if err := a.readiness.CheckReady(r.Context()); err != nil {
		a.logger.Warn("readiness check failed", "error", err)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
