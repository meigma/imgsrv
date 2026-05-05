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
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/jobs"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/store/postgres"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	serverStartupTimeout        = 5 * time.Second
	serverShutdownTimeout       = 5 * time.Second
	serverReadyInterval         = 10 * time.Millisecond
	casPromotionWorkerNodeName  = "imgsrv-integration"
	casPromotionWorkerRunID     = "test"
	casPromotionWorkerName      = "cas-promotion"
	casPromotionPollInterval    = 10 * time.Millisecond
	casPromotionErrorBackoffMax = 100 * time.Millisecond
)

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

	server, err := app.NewServer(app.Config{
		Listen:          listener.Addr().String(),
		ShutdownTimeout: serverShutdownTimeout,
	}, app.Dependencies{
		Logger: options.logger,
		Uploads: uploads.NewService(uploads.ServiceConfig{
			Store:   store.Uploads(),
			Objects: objects,
		}),
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

func backgroundJobs(
	options options,
	store *postgres.Store,
	objects objectstore.Store,
) []app.BackgroundJob {
	if !options.casPromotion {
		return nil
	}

	casService := cas.NewService(cas.ServiceConfig{
		Store:   store.CAS(),
		Objects: objects,
	})

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
