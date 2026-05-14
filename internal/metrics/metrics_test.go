package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/metrics"
	"github.com/meigma/imgsrv/internal/telemetry"
)

func TestNoopRecorderDropsObservations(t *testing.T) {
	ctx := context.Background()
	recorder := metrics.Noop()

	require.False(t, recorder.Enabled())
	require.NoError(t, recorder.RegisterPostgres(fakePostgresObserver{}))
	recorder.RecordObjectstoreOperation(ctx, "put_part", "success", time.Millisecond)
	recorder.RecordObjectstoreBytes(ctx, "put_part", "write", 12)
	recorder.RecordBackgroundJobAttempt(ctx, "publish", "worked")
	recorder.RecordBackgroundJobError(ctx, "publish")
	recorder.RecordBackgroundJobCircuitOpen(ctx, "publish")
	recorder.RecordBackgroundJobConsecutiveFailures(ctx, "publish", 2)
}

func TestRecorderExportsApplicationMetrics(t *testing.T) {
	ctx := context.Background()
	providers, recorder := newMetricsRecorder(t)
	recorder.RecordObjectstoreOperation(ctx, "put_part", "success", 15*time.Millisecond)
	recorder.RecordObjectstoreBytes(ctx, "put_part", "write", 42)
	recorder.RecordBackgroundJobAttempt(ctx, "publish", "worked")
	recorder.RecordBackgroundJobError(ctx, "publish")
	recorder.RecordBackgroundJobCircuitOpen(ctx, "publish")
	recorder.RecordBackgroundJobConsecutiveFailures(ctx, "publish", 1)
	require.NoError(t, recorder.RegisterPostgres(fakePostgresObserver{
		pool: metrics.PostgresPoolSnapshot{
			AcquiredConns:        1,
			IdleConns:            2,
			ConstructingConns:    0,
			TotalConns:           3,
			MaxConns:             4,
			AcquireCount:         5,
			EmptyAcquireCount:    6,
			CanceledAcquireCount: 7,
			AcquireDuration:      8 * time.Second,
		},
		store: metrics.StoreSnapshot{
			UploadSessions: []metrics.StateCount{
				{State: "completed", Count: 1},
			},
			CASIngestJobs: []metrics.StateCount{
				{State: "running", Count: 1},
			},
			CASIngestOldestRunningAge:    5 * time.Minute,
			HasCASIngestOldestRunningAge: true,
			CASBlobs:                     2,
			CASBlobBytes:                 42,
			PublishJobs: []metrics.StateCount{
				{State: "running", Count: 1},
			},
			PublishSteps: []metrics.StepStateCount{
				{Step: "validate_catalog", State: "running", Count: 1},
			},
			PublishStepOldestRunningAges: []metrics.StepAge{
				{Step: "validate_catalog", Age: 10 * time.Second},
			},
			PublishingVersions:  1,
			IncusProjectionRows: 3,
		},
	}))

	body := scrapeMetrics(t, providers)

	assert.Contains(t, body, "imgsrv_objectstore_operations_total")
	assert.Contains(t, body, `operation="put_part"`)
	assert.Contains(t, body, `outcome="success"`)
	assert.Contains(t, body, "imgsrv_objectstore_bytes_total")
	assert.Contains(t, body, `direction="write"`)
	assert.Contains(t, body, "imgsrv_background_job_attempts_total")
	assert.Contains(t, body, `job="publish"`)
	assert.Contains(t, body, "imgsrv_background_job_circuit_open_total")
	assert.Contains(t, body, "imgsrv_background_job_last_success_timestamp_seconds")
	assert.Contains(t, body, "imgsrv_background_job_last_error_timestamp_seconds")
	assert.Contains(t, body, `state="acquired"`)
	assert.Contains(t, body, "imgsrv_postgres_pool_connections")
	assert.Contains(t, body, "imgsrv_postgres_pool_acquire_duration_seconds_total")
	assert.Contains(t, body, "imgsrv_upload_sessions")
	assert.Contains(t, body, "imgsrv_cas_ingest_oldest_running_age_seconds")
	assert.Contains(t, body, "imgsrv_publish_steps")
	assert.Contains(t, body, `step="validate_catalog"`)
	assert.Contains(t, body, "imgsrv_incus_projection_rows")
}

func newMetricsRecorder(t *testing.T) (*telemetry.Telemetry, *metrics.Recorder) {
	t.Helper()

	providers, err := telemetry.New(telemetry.Config{MetricsPath: "/metrics"})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, providers.Shutdown(context.Background()))
	})
	recorder, err := metrics.New(providers.Meter("github.com/meigma/imgsrv/internal/metrics_test"))
	require.NoError(t, err)

	return providers, recorder
}

func scrapeMetrics(t *testing.T, providers *telemetry.Telemetry) string {
	t.Helper()

	rec := httptest.NewRecorder()
	providers.MetricsHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	return rec.Body.String()
}

type fakePostgresObserver struct {
	pool  metrics.PostgresPoolSnapshot
	store metrics.StoreSnapshot
}

func (observer fakePostgresObserver) PoolMetrics() (metrics.PostgresPoolSnapshot, error) {
	return observer.pool, nil
}

func (observer fakePostgresObserver) StoreMetrics(context.Context) (metrics.StoreSnapshot, error) {
	return observer.store, nil
}
