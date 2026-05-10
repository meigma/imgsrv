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
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
	"github.com/meigma/imgsrv/internal/store/postgres"
)

const (
	// defaultHTTPClientTimeout bounds requests issued by the harness HTTP client.
	defaultHTTPClientTimeout = 10 * time.Second

	integrationWriterRoleID = "integration-content-writer"
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

// WithOIDC configures generic OIDC JWT bearer authentication for the test server.
func WithOIDC(
	issuerURL string,
	audience string,
	requiredScope string,
	httpClients ...*http.Client,
) Option {
	return func(options *options) {
		options.oidcIssuerURL = issuerURL
		options.oidcAudience = audience
		options.oidcRequiredScope = requiredScope
		options.useOIDCHTTPClient(httpClients...)
	}
}

// WithGitHubActionsOIDC configures GitHub Actions OIDC publisher authentication.
func WithGitHubActionsOIDC(
	issuerURL string,
	audience string,
	repositoryID string,
	workflowRef string,
	subject string,
	httpClients ...*http.Client,
) Option {
	return func(options *options) {
		options.githubOIDCIssuerURL = issuerURL
		options.githubOIDCAudience = audience
		options.githubOIDCRepositoryID = repositoryID
		options.githubOIDCWorkflowRef = workflowRef
		options.githubOIDCSubject = subject
		options.useOIDCHTTPClient(httpClients...)
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

	// apiToken is the plaintext generated authkit API token seeded for this environment.
	apiToken string
}

// Start creates a full imgsrv integration-test environment.
func Start(t testing.TB, opts ...Option) *Env {
	t.Helper()

	ctx := t.Context()
	startupOptions := newOptions(opts...)
	postgresURL := startPostgres(ctx, t)
	s3Config := startGarage(ctx, t)
	store := openStore(ctx, t, postgresURL)
	apiToken := seedAPIToken(ctx, t, store, startupOptions.apiToken)
	objectStore := openObjectStore(t, s3Config)
	baseURL := startServer(ctx, t, startupOptions, store, objectStore)

	return &Env{
		baseURL:     baseURL,
		httpClient:  newHTTPClient(),
		store:       store,
		objectStore: objectStore,
		s3Config:    s3Config,
		apiToken:    apiToken,
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

// APIToken returns the generated plaintext API token seeded for this environment.
func (env *Env) APIToken() string {
	return env.apiToken
}

type options struct {
	logger                 *slog.Logger
	casPromotion           bool
	apiToken               bool
	oidcIssuerURL          string
	oidcAudience           string
	oidcRequiredScope      string
	githubOIDCIssuerURL    string
	githubOIDCAudience     string
	githubOIDCRepositoryID string
	githubOIDCWorkflowRef  string
	githubOIDCSubject      string
	oidcHTTPClient         *http.Client
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
	role, err := authStore.CreateRole(ctx, authkit.CreateRoleRequest{
		ID:          integrationWriterRoleID,
		DisplayName: "Integration content writer",
		Description: "Allows integration tests to write imgsrv content.",
	})
	require.NoError(t, err)
	require.NoError(t, authStore.GrantRoleAction(ctx, authkit.GrantRoleActionRequest{
		RoleID: role.ID,
		Action: authz.ActionContentWrite,
	}))
	principal, err := authStore.CreatePrincipal(ctx, authkit.CreatePrincipalRequest{
		Kind:        authkit.PrincipalKindService,
		DisplayName: "integration-test",
		Attributes: map[string]any{
			"source": "integration",
		},
	})
	require.NoError(t, err)
	require.NoError(t, authStore.AssignPrincipalRole(ctx, authkit.AssignPrincipalRoleRequest{
		PrincipalID: principal.ID,
		RoleID:      role.ID,
	}))
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
