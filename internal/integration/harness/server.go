//go:build integration

package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/meigma/authkit/httpauth"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/app"
	"github.com/meigma/imgsrv/internal/authz"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/httpapi"
	"github.com/meigma/imgsrv/internal/jobs"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/jobs/publishflow"
	incusmaterialization "github.com/meigma/imgsrv/internal/materialization/incus"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/publish"
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

	// publishWorkerName is the job name suffix used in the worker ID and in
	// logger attributes for the durable publish job.
	publishWorkerName = "publish"

	// publishPollInterval is the poll cadence the integration publish job uses
	// when looking for queued publish steps.
	publishPollInterval = 10 * time.Millisecond

	// publishErrorBackoffMax caps the exponential backoff applied after publish
	// worker failures during integration runs.
	publishErrorBackoffMax = 100 * time.Millisecond
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
	authStore := store.Authkit()
	require.NoError(t, authz.EnsureBuiltinRoles(ctx, authStore))
	require.NoError(t, authz.EnsureBootstrapAdmin(ctx, authz.BootstrapConfig{
		Store:  authStore,
		Output: options.bootstrapOutput,
		Logger: options.logger.With("component", "authz"),
	}))
	authService := newAuthService(ctx, t, options, store)
	authManagement := authz.NewManagementService(authz.ManagementConfig{
		Store:      authStore,
		HTTPClient: options.oidcHTTPClient,
		Logger:     options.logger.With("component", "auth-management"),
	})

	catalogService := catalog.NewService(catalog.ServiceConfig{
		Store: store.Catalog(),
	})
	blobService := newCASService(store, objects, options.logger.With("component", "cas"))
	server, err := app.NewServer(app.Config{
		Listen:          listener.Addr().String(),
		ShutdownTimeout: serverShutdownTimeout,
	}, app.Dependencies{
		Logger:         options.logger,
		Auth:           authService,
		AuthManagement: authManagement,
		Uploads: uploads.NewService(uploads.ServiceConfig{
			Store:        store.Uploads(),
			Objects:      objects,
			TrustedBlobs: trustedBlobLookup(store),
			Logger:       options.logger.With("component", "uploads"),
		}),
		Catalog: catalogService,
		Publish: publish.NewService(publish.ServiceConfig{
			Store: store.Publish(),
		}),
		Blobs: blobService,
		SimpleStreams: incusmaterialization.NewService(incusmaterialization.Config{
			Catalog: catalogService,
			Store:   store.IncusProjection(),
			Logger:  options.logger.With("component", "incus-materialization"),
		}),
		BackgroundJobs: backgroundJobs(options, store, objects, catalogService, blobService),
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
) *httpauth.Middleware {
	t.Helper()

	authMiddleware, err := authz.NewMiddleware(ctx, authz.Config{
		Store:         store.Authkit(),
		HTTPClient:    options.oidcHTTPClient,
		ErrorRenderer: httpapi.NewAuthErrorRenderer(options.logger.With("component", "httpapi")),
		Logger:        options.logger.With("component", "authz"),
	})
	require.NoError(t, err)

	return authMiddleware
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
	catalogService httpapi.CatalogService,
	blobService httpapi.BlobService,
) []app.BackgroundJob {
	publishJob := jobs.New(jobs.Config{
		Handler: publishflow.New(publishflow.Config{
			Store: store.Publish(),
			Incus: incusmaterialization.NewIndexer(incusmaterialization.IndexerConfig{
				Catalog: catalogService,
				Blobs:   blobService,
				Logger:  options.logger.With("component", "incus-indexer"),
			}),
			Logger: options.logger.With("component", publishWorkerName),
		}),
		WorkerID: jobs.Identity{
			NodeName: casPromotionWorkerNodeName,
			RunID:    casPromotionWorkerRunID,
		}.WorkerID(publishWorkerName),
		Interval:            publishPollInterval,
		ErrorBackoffInitial: publishPollInterval,
		ErrorBackoffMax:     publishErrorBackoffMax,
		Logger:              options.logger.With("component", publishWorkerName),
	})
	if !options.casPromotion {
		return []app.BackgroundJob{publishJob}
	}

	casService := newCASService(store, objects, options.logger.With("component", "cas"))

	return []app.BackgroundJob{jobs.New(jobs.Config{
		Handler: promote.New(promote.Config{
			Uploads: store.Uploads(),
			CAS:     casService,
			Logger:  options.logger.With("component", casPromotionWorkerName),
		}),
		WorkerID: jobs.Identity{
			NodeName: casPromotionWorkerNodeName,
			RunID:    casPromotionWorkerRunID,
		}.WorkerID(casPromotionWorkerName),
		Interval:            casPromotionPollInterval,
		ErrorBackoffInitial: casPromotionPollInterval,
		ErrorBackoffMax:     casPromotionErrorBackoffMax,
		Logger:              options.logger.With("component", casPromotionWorkerName),
	}), publishJob}
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
func newCASService(store *postgres.Store, objects objectstore.Store, logger *slog.Logger) *cas.Service {
	return cas.NewService(cas.ServiceConfig{
		Store:   store.CAS(),
		Objects: objects,
		Logger:  logger,
	})
}
