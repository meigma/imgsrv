//go:build integration

// Package harness starts full imgsrv integration-test environments.
package harness

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
	"github.com/meigma/imgsrv/internal/store/postgres"
)

const (
	// defaultHTTPClientTimeout bounds requests issued by the harness HTTP client.
	defaultHTTPClientTimeout = 10 * time.Second
)

// Option customizes integration environment startup.
type Option func(*options)

// WithLogger sets the logger used by the in-process imgsrv server.
func WithLogger(logger *slog.Logger) Option {
	return func(options *options) {
		if logger == nil {
			return
		}
		options.logger = logger
	}
}

// WithCASPromotion starts the real in-process CAS promotion worker.
func WithCASPromotion() Option {
	return func(options *options) {
		options.casPromotion = true
	}
}

// Env owns a running imgsrv integration-test environment.
type Env struct {
	// baseURL is the root URL for the in-process imgsrv HTTP server.
	baseURL string

	// httpClient is the HTTP client integration tests use to call the server.
	httpClient *http.Client

	// store is the migrated Postgres store backing the environment.
	store *postgres.Store

	// objectStore is the Garage-backed object store wired into the server.
	objectStore objectstore.Store

	// s3Config holds the S3-compatible storage configuration for Garage.
	s3Config s3.Config
}

// Start creates a full imgsrv integration-test environment.
func Start(t testing.TB, opts ...Option) *Env {
	t.Helper()

	ctx := t.Context()
	startupOptions := newOptions(opts...)
	postgresURL := startPostgres(ctx, t)
	s3Config := startGarage(ctx, t)
	store := openStore(ctx, t, postgresURL)
	objectStore := openObjectStore(t, s3Config)
	baseURL := startServer(ctx, t, startupOptions, store, objectStore)

	return &Env{
		baseURL:     baseURL,
		httpClient:  newHTTPClient(),
		store:       store,
		objectStore: objectStore,
		s3Config:    s3Config,
	}
}

// BaseURL returns the root URL for the in-process imgsrv HTTP server.
func (env *Env) BaseURL() string {
	return env.baseURL
}

// URL returns an absolute server URL for path.
func (env *Env) URL(path string) string {
	if strings.HasPrefix(path, "/") {
		return env.baseURL + path
	}

	return env.baseURL + "/" + path
}

// HTTPClient returns the client tests should use for HTTP API calls.
func (env *Env) HTTPClient() *http.Client {
	return env.httpClient
}

// Store returns the migrated Postgres store backing the environment.
func (env *Env) Store() *postgres.Store {
	return env.store
}

// ObjectStore returns the Garage-backed object store used by the service.
func (env *Env) ObjectStore() objectstore.Store {
	return env.objectStore
}

// S3Config returns the S3-compatible storage configuration for Garage.
func (env *Env) S3Config() s3.Config {
	return env.s3Config
}

type options struct {
	logger       *slog.Logger
	casPromotion bool
}

// newOptions applies opts to a zero options value and returns the resolved
// startup configuration with a no-op logger as the default.
func newOptions(opts ...Option) options {
	result := options{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(&result)
	}

	return result
}

// newHTTPClient builds the HTTP client integration tests use to talk to the
// in-process imgsrv server.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPClientTimeout}
}
