//go:build integration

package harness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/app"
	"github.com/meigma/imgsrv/internal/auth"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/jobs"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/store/postgres"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	// serverStartupTimeout bounds how long the harness waits for the in-process
	// HTTP server to become ready.
	serverStartupTimeout = 5 * time.Second

	// serverShutdownTimeout bounds how long the harness gives the server to
	// drain on test teardown.
	serverShutdownTimeout = 5 * time.Second

	// serverReadyInterval is the polling interval used while waiting for the
	// server health endpoint to respond.
	serverReadyInterval = 10 * time.Millisecond

	// casPromotionWorkerNodeName is the static node name used in the worker ID
	// for the integration CAS promotion job.
	casPromotionWorkerNodeName = "imgsrv-integration"

	// casPromotionWorkerRunID is the static run ID used in the worker ID for
	// the integration CAS promotion job.
	casPromotionWorkerRunID = "test"

	// casPromotionWorkerName is the job name suffix used in the worker ID and
	// in logger attributes for the CAS promotion job.
	casPromotionWorkerName = "cas-promotion"

	// casPromotionPollInterval is the poll cadence the integration CAS
	// promotion job uses when looking for queued ingest jobs.
	casPromotionPollInterval = 10 * time.Millisecond

	// casPromotionErrorBackoffMax caps the exponential backoff applied after
	// CAS promotion failures during integration runs.
	casPromotionErrorBackoffMax = 100 * time.Millisecond
)

// startServer constructs and runs the in-process imgsrv HTTP server, waits for
// it to become ready, and returns its base URL. It registers a cleanup that
// shuts the server down at test teardown.
func startServer(
	ctx context.Context,
	t testing.TB,
	options options,
	store *postgres.Store,
	objects objectstore.Store,
) string {
	t.Helper()

	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	authService := newAuthService(ctx, t, options, store)

	server, err := app.NewServer(app.Config{
		Listen:          listener.Addr().String(),
		ShutdownTimeout: serverShutdownTimeout,
	}, app.Dependencies{
		Logger: options.logger,
		Auth:   authService,
		Uploads: uploads.NewService(uploads.ServiceConfig{
			Store:        store.Uploads(),
			Objects:      objects,
			TrustedBlobs: trustedBlobLookup(store),
		}),
		Catalog: catalog.NewService(catalog.ServiceConfig{
			Store: store.Catalog(),
		}),
		Blobs:          newCASService(store, objects),
		BackgroundJobs: backgroundJobs(options, store, objects),
	})
	require.NoError(t, err)

	serverCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(serverCtx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(serverShutdownTimeout):
			require.NoError(t, fmt.Errorf("imgsrv server did not shut down within %s", serverShutdownTimeout))
		}
	})

	baseURL := "http://" + listener.Addr().String()
	waitForServer(ctx, t, baseURL, errCh)

	return baseURL
}

// newAuthService constructs the chained auth service used by integration servers.
func newAuthService(
	ctx context.Context,
	t testing.TB,
	options options,
	store *postgres.Store,
) *auth.Service {
	t.Helper()

	var authenticators []auth.Authenticator
	if options.githubOIDCIssuerURL != "" ||
		options.githubOIDCAudience != "" ||
		options.githubOIDCRepositoryID != "" ||
		options.githubOIDCWorkflowRef != "" {
		githubAuthenticator, err := auth.NewGitHubActionsOIDCAuthenticator(
			ctx,
			auth.GitHubActionsOIDCConfig{
				IssuerURL:    options.githubOIDCIssuerURL,
				Audience:     options.githubOIDCAudience,
				RepositoryID: options.githubOIDCRepositoryID,
				WorkflowRef:  options.githubOIDCWorkflowRef,
			},
		)
		require.NoError(t, err)
		authenticators = append(authenticators, githubAuthenticator)
	}
	if options.oidcIssuerURL != "" || options.oidcAudience != "" || options.oidcRequiredScope != "" {
		oidcAuthenticator, err := auth.NewOIDCAuthenticator(ctx, auth.OIDCConfig{
			IssuerURL:     options.oidcIssuerURL,
			Audience:      options.oidcAudience,
			RequiredScope: options.oidcRequiredScope,
		})
		require.NoError(t, err)
		authenticators = append(authenticators, oidcAuthenticator)
	}

	return auth.NewService(auth.ServiceConfig{
		Store:          store.Auth(),
		Authenticators: authenticators,
	})
}

// waitForServer polls the server health endpoint until it returns 204, the
// server exits early via errCh, or the startup timeout elapses.
func waitForServer(ctx context.Context, t testing.TB, baseURL string, errCh <-chan error) {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, serverStartupTimeout)
	defer cancel()

	client := newHTTPClient()
	ticker := time.NewTicker(serverReadyInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		req, err := http.NewRequestWithContext(waitCtx, http.MethodGet, baseURL+"/healthz", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
			lastErr = fmt.Errorf("healthz returned %s", resp.Status)
		} else {
			lastErr = err
		}

		select {
		case err := <-errCh:
			require.NoError(t, err)
			require.NoError(t, errors.New("imgsrv server exited before readiness"))
		case <-waitCtx.Done():
			require.NoError(t, fmt.Errorf("wait for imgsrv readiness: %w", lastErr))
		case <-ticker.C:
		}
	}
}

// backgroundJobs builds the optional in-process background jobs the harness
// runs alongside the HTTP server. It returns nil unless CAS promotion was
// requested via WithCASPromotion.
func backgroundJobs(
	options options,
	store *postgres.Store,
	objects objectstore.Store,
) []app.BackgroundJob {
	if !options.casPromotion {
		return nil
	}

	casService := newCASService(store, objects)

	return []app.BackgroundJob{jobs.New(jobs.Config{
		Handler: promote.New(promote.Config{
			Uploads: store.Uploads(),
			CAS:     casService,
		}),
		WorkerID: jobs.Identity{
			NodeName: casPromotionWorkerNodeName,
			RunID:    casPromotionWorkerRunID,
		}.WorkerID(casPromotionWorkerName),
		Interval:            casPromotionPollInterval,
		ErrorBackoffInitial: casPromotionPollInterval,
		ErrorBackoffMax:     casPromotionErrorBackoffMax,
		Logger:              options.logger.With("component", casPromotionWorkerName),
	})}
}

// trustedBlobLookup returns the upload trusted-blob lookup when the shared store implements it.
func trustedBlobLookup(store *postgres.Store) uploads.TrustedBlobLookup {
	lookup, ok := store.Uploads().(uploads.TrustedBlobLookup)
	if !ok {
		return nil
	}

	return lookup
}

// newCASService constructs the CAS service used by HTTP reads and promotion jobs.
func newCASService(store *postgres.Store, objects objectstore.Store) *cas.Service {
	return cas.NewService(cas.ServiceConfig{
		Store:   store.CAS(),
		Objects: objects,
	})
}
