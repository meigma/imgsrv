package jobs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/jobs"
	appmetrics "github.com/meigma/imgsrv/internal/metrics"
	"github.com/meigma/imgsrv/internal/telemetry"
)

func TestRunnerRunOnceExecutesOneAttempt(t *testing.T) {
	handler := &recordingHandler{
		run: func(_ context.Context, workerID string) (jobs.Result, error) {
			assert.Equal(t, "worker-a", workerID)
			return jobs.Result{Worked: true}, nil
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:  handler,
		WorkerID: "worker-a",
	})

	got, err := runner.RunOnce(context.Background())

	require.NoError(t, err)
	assert.True(t, got.Worked)
	assert.Equal(t, 1, handler.calls)
}

func TestRunnerRunOnceTreatsIdleAsSuccess(t *testing.T) {
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			return jobs.Result{}, nil
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:  handler,
		WorkerID: "worker-a",
	})

	got, err := runner.RunOnce(context.Background())

	require.NoError(t, err)
	assert.False(t, got.Worked)
	assert.Equal(t, 1, handler.calls)
}

func TestRunnerRunExitsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			cancel()
			return jobs.Result{Worked: true}, nil
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:  handler,
		WorkerID: "worker-a",
		Interval: time.Hour,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, handler.calls)
}

func TestRunnerRunContinuesAfterAttemptError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wantErr := errors.New("temporary failure")
	attempts := 0
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			attempts++
			if attempts == 1 {
				return jobs.Result{}, wantErr
			}
			cancel()
			return jobs.Result{}, nil
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:             handler,
		WorkerID:            "worker-a",
		Interval:            time.Hour,
		ErrorBackoffInitial: time.Nanosecond,
		ErrorBackoffMax:     time.Nanosecond,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	assert.Equal(t, 2, handler.calls)
}

func TestRunnerRunOpensCircuitAfterConfiguredFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	wantErr := errors.New("temporary failure")
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			return jobs.Result{}, wantErr
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:                handler,
		WorkerID:               "worker-a",
		ErrorBackoffInitial:    time.Nanosecond,
		ErrorBackoffMax:        time.Nanosecond,
		CircuitBreakerFailures: 1,
		CircuitBreakerCooldown: time.Hour,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, handler.calls)
}

func TestRunnerRunOnceSurfacesAttemptError(t *testing.T) {
	wantErr := errors.New("temporary failure")
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			return jobs.Result{}, wantErr
		},
	}
	runner := jobs.New(jobs.Config{
		Handler:  handler,
		WorkerID: "worker-a",
	})

	got, err := runner.RunOnce(context.Background())

	require.ErrorIs(t, err, wantErr)
	assert.False(t, got.Worked)
	assert.Equal(t, 1, handler.calls)
}

func TestRunnerRunOnceRecordsMetrics(t *testing.T) {
	providers, recorder := newJobMetricsRecorder(t)
	wantErr := errors.New("temporary failure")
	attempts := 0
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			attempts++
			if attempts == 1 {
				return jobs.Result{Worked: true}, nil
			}
			if attempts == 2 {
				return jobs.Result{}, nil
			}
			return jobs.Result{}, wantErr
		},
	}
	runner := jobs.New(jobs.Config{
		Name:     "publish",
		Handler:  handler,
		WorkerID: "worker-a",
		Metrics:  recorder,
	})

	_, err := runner.RunOnce(context.Background())
	require.NoError(t, err)
	_, err = runner.RunOnce(context.Background())
	require.NoError(t, err)
	_, err = runner.RunOnce(context.Background())
	require.ErrorIs(t, err, wantErr)

	body := scrapeJobMetrics(t, providers)

	assert.Contains(t, body, `job="publish"`)
	assert.Contains(t, body, `outcome="worked"`)
	assert.Contains(t, body, `outcome="idle"`)
	assert.Contains(t, body, `outcome="error"`)
	assert.Contains(t, body, "imgsrv_background_job_errors_total")
	assert.Contains(t, body, "imgsrv_background_job_consecutive_failures")
}

func TestRunnerRunRecordsCircuitBreakerMetrics(t *testing.T) {
	providers, recorder := newJobMetricsRecorder(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	wantErr := errors.New("temporary failure")
	handler := &recordingHandler{
		run: func(context.Context, string) (jobs.Result, error) {
			return jobs.Result{}, wantErr
		},
	}
	runner := jobs.New(jobs.Config{
		Name:                   "cas-promotion",
		Handler:                handler,
		WorkerID:               "worker-a",
		ErrorBackoffInitial:    time.Nanosecond,
		ErrorBackoffMax:        time.Nanosecond,
		CircuitBreakerFailures: 1,
		CircuitBreakerCooldown: time.Hour,
		Metrics:                recorder,
	})

	err := runner.Run(ctx)

	require.NoError(t, err)
	body := scrapeJobMetrics(t, providers)
	assert.Contains(t, body, `job="cas-promotion"`)
	assert.Contains(t, body, "imgsrv_background_job_circuit_open_total")
}

type recordingHandler struct {
	calls int
	run   func(context.Context, string) (jobs.Result, error)
}

func (handler *recordingHandler) RunOnce(ctx context.Context, workerID string) (jobs.Result, error) {
	result, err := handler.run(ctx, workerID)
	handler.calls++
	return result, err
}

func newJobMetricsRecorder(t *testing.T) (*telemetry.Telemetry, *appmetrics.Recorder) {
	t.Helper()

	providers, err := telemetry.New(telemetry.Config{MetricsPath: "/metrics"})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, providers.Shutdown(context.Background()))
	})
	recorder, err := appmetrics.New(providers.Meter("github.com/meigma/imgsrv/internal/jobs_test"))
	require.NoError(t, err)

	return providers, recorder
}

func scrapeJobMetrics(t *testing.T, providers *telemetry.Telemetry) string {
	t.Helper()

	rec := httptest.NewRecorder()
	providers.MetricsHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "imgsrv_background_job", "expected job metrics, got:\n%s", body)

	return body
}
