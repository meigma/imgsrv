//go:build integration

package imgsrvtest

import (
	"log/slog"
	"net/http"
	"testing"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/integration/harness"
)

// Option customizes imgsrv test environment startup.
type Option func(*options)

// WithLogger sets the logger used by the in-process imgsrv server.
func WithLogger(logger *slog.Logger) Option {
	return func(options *options) {
		options.harness = append(options.harness, harness.WithLogger(logger))
	}
}

// WithCASPromotion starts the real in-process CAS promotion worker.
func WithCASPromotion() Option {
	return func(options *options) {
		options.harness = append(options.harness, harness.WithCASPromotion())
	}
}

// Env owns a running imgsrv functional-test environment.
type Env struct {
	harness *harness.Env
}

// Start creates a disposable imgsrv functional-test environment.
func Start(t testing.TB, opts ...Option) *Env {
	t.Helper()

	startupOptions := newOptions(opts...)

	return &Env{
		harness: harness.Start(t, startupOptions.harness...),
	}
}

// BaseURL returns the root URL for the imgsrv HTTP server.
func (env *Env) BaseURL() string {
	return env.harness.BaseURL()
}

// URL returns an absolute server URL for path.
func (env *Env) URL(path string) string {
	return env.harness.URL(path)
}

// HTTPClient returns the HTTP client tests should use for imgsrv API calls.
func (env *Env) HTTPClient() *http.Client {
	return env.harness.HTTPClient()
}

// ClientOptions returns client SDK options for this environment.
func (env *Env) ClientOptions() imgsrv.Options {
	return imgsrv.Options{
		BaseURL:    env.BaseURL(),
		HTTPClient: env.HTTPClient(),
	}
}

// Client returns a Go SDK client for this environment.
func (env *Env) Client(t testing.TB) *imgsrv.Client {
	t.Helper()

	client, err := imgsrv.New(env.ClientOptions())
	if err != nil {
		t.Fatalf("create imgsrv client: %v", err)
	}

	return client
}

type options struct {
	harness []harness.Option
}

func newOptions(opts ...Option) options {
	var result options
	for _, opt := range opts {
		opt(&result)
	}

	return result
}
