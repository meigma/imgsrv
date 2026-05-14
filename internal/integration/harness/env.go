//go:build integration

// Package harness starts full imgsrv integration-test environments.
package harness

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/apikey"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/authz"
	appmetrics "github.com/meigma/imgsrv/internal/metrics"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
	"github.com/meigma/imgsrv/internal/store/postgres"
	"github.com/meigma/imgsrv/internal/telemetry"
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

// WithAPIToken seeds a generated API token for write-route authentication.
func WithAPIToken() Option {
	return func(options *options) {
		options.apiToken = true
	}
}

// WithOIDCHTTPClient configures the HTTP client used for OIDC discovery and JWKS requests.
func WithOIDCHTTPClient(httpClient *http.Client) Option {
	return func(options *options) {
		options.useOIDCHTTPClient(httpClient)
	}
}

// WithBootstrapOutput captures the one-time bootstrap token printed on first startup.
func WithBootstrapOutput(output io.Writer) Option {
	return func(options *options) {
		if output == nil {
			return
		}
		options.bootstrapOutput = output
	}
}

// WithMetrics starts the in-process metrics listener for the integration environment.
func WithMetrics() Option {
	return func(options *options) {
		options.metrics = true
	}
}

// Env owns a running imgsrv integration-test environment.
type Env struct {
	// baseURL is the root URL for the in-process imgsrv HTTP server.
	baseURL string

	// metricsURL is the root URL for the in-process metrics server.
	metricsURL string

	// httpClient is the HTTP client integration tests use to call the server.
	httpClient *http.Client

	// store is the migrated Postgres store backing the environment.
	store *postgres.Store

	// objectStore is the Garage-backed object store wired into the server.
	objectStore objectstore.Store

	// s3Config holds the S3-compatible storage configuration for Garage.
	s3Config s3.Config

	// apiToken is the plaintext generated authkit API token seeded for this environment.
	apiToken string

	// metrics is the optional recorder used by the in-process server.
	metrics *appmetrics.Recorder

	// telemetry is the optional telemetry provider backing the metrics listener.
	telemetry *telemetry.Telemetry
}

// Start creates a full imgsrv integration-test environment.
func Start(t testing.TB, opts ...Option) *Env {
	t.Helper()

	ctx := t.Context()
	deps, err := StartDependencies(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, deps.Close(context.Background()))
	})

	return StartWithDependencies(t, deps, opts...)
}

// StartWithDependencies creates a full imgsrv integration-test environment
// using already-running shared dependencies.
func StartWithDependencies(t testing.TB, deps *Dependencies, opts ...Option) *Env {
	t.Helper()
	require.NotNil(t, deps)

	ctx := t.Context()
	startupOptions := newOptions(opts...)
	isolation := newIsolationNames(t)
	store := openIsolatedStore(ctx, t, deps.postgresURL, isolation.schema)
	apiToken := seedAPIToken(ctx, t, store, startupOptions.apiToken)
	baseObjectStore := openObjectStore(t, deps.s3Config)
	objectStore := newPrefixedObjectStore(baseObjectStore, isolation.objectPrefix)
	observability := startObservability(t, startupOptions, store)
	objectStore = objectstore.InstrumentStore(objectStore, observability.metrics)
	endpoints := startServer(ctx, t, startupOptions, store, objectStore, observability)

	return &Env{
		baseURL:     endpoints.baseURL,
		metricsURL:  endpoints.metricsURL,
		httpClient:  newHTTPClient(),
		store:       store,
		objectStore: objectStore,
		s3Config:    deps.s3Config,
		apiToken:    apiToken,
		metrics:     observability.metrics,
		telemetry:   observability.telemetry,
	}
}

// BaseURL returns the root URL for the in-process imgsrv HTTP server.
func (env *Env) BaseURL() string {
	return env.baseURL
}

// MetricsURL returns the root URL for the metrics listener.
func (env *Env) MetricsURL() string {
	return env.metricsURL
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

// APIToken returns the generated plaintext API token seeded for this environment.
func (env *Env) APIToken() string {
	return env.apiToken
}

type options struct {
	logger          *slog.Logger
	bootstrapOutput io.Writer
	casPromotion    bool
	apiToken        bool
	metrics         bool
	oidcHTTPClient  *http.Client
}

type observability struct {
	telemetry *telemetry.Telemetry
	metrics   *appmetrics.Recorder
}

func startObservability(t testing.TB, options options, store *postgres.Store) observability {
	t.Helper()
	if !options.metrics {
		return observability{metrics: appmetrics.Noop()}
	}

	providers, err := telemetry.New(telemetry.Config{
		ServiceName: "imgsrv",
		MetricsPath: "/metrics",
	})
	require.NoError(t, err)

	recorder, err := appmetrics.New(providers.Meter("github.com/meigma/imgsrv/internal/metrics"))
	require.NoError(t, err)
	require.NoError(t, recorder.RegisterPostgres(store))

	return observability{
		telemetry: providers,
		metrics:   recorder,
	}
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

func (options *options) useOIDCHTTPClient(httpClients ...*http.Client) {
	if len(httpClients) == 0 || httpClients[0] == nil {
		return
	}
	options.oidcHTTPClient = httpClients[0]
}

// seedAPIToken inserts a generated authkit API token when configured.
func seedAPIToken(ctx context.Context, t testing.TB, store *postgres.Store, enabled bool) string {
	t.Helper()
	if !enabled {
		return ""
	}

	authStore := store.Authkit()
	require.NoError(t, authz.EnsureBuiltinRoles(ctx, authStore))
	principal, err := authStore.CreatePrincipal(ctx, authkit.CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: "integration-test",
		Attributes: map[string]any{
			"source": "integration",
		},
	})
	require.NoError(t, err)
	for _, roleID := range []string{authz.RoleContentWriter, authz.RoleAuthManager} {
		require.NoError(t, authStore.AssignPrincipalRole(ctx, authkit.AssignPrincipalRoleRequest{
			PrincipalID: principal.ID,
			RoleID:      roleID,
		}))
	}
	apiTokens, err := apikey.NewService(authStore)
	require.NoError(t, err)
	issued, err := apiTokens.IssueToken(ctx, apikey.IssueRequest{
		PrincipalID: principal.ID,
		Name:        "integration test",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = authStore.LinkIdentity(ctx, issued.IdentityLink)
	require.NoError(t, err)

	return issued.Plaintext
}

// newHTTPClient builds the HTTP client integration tests use to talk to the
// in-process imgsrv server.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPClientTimeout}
}
