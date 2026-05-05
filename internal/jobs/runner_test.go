package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/jobs"
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

type recordingHandler struct {
	calls int
	run   func(context.Context, string) (jobs.Result, error)
}

func (handler *recordingHandler) RunOnce(ctx context.Context, workerID string) (jobs.Result, error) {
	result, err := handler.run(ctx, workerID)
	handler.calls++
	return result, err
}
