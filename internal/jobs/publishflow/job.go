// Package publishflow claims durable publish steps and executes their handlers.
package publishflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/meigma/imgsrv/internal/jobs"
	"github.com/meigma/imgsrv/internal/materialization/incus"
	"github.com/meigma/imgsrv/internal/publish"
)

const defaultStaleStepTimeout = 5 * time.Minute

// StepStore provides durable publish-step operations.
type StepStore interface {
	// ClaimStep claims the next runnable publish step for a worker.
	ClaimStep(context.Context, publish.ClaimStepParams) (publish.Step, error)

	// SucceedValidateCatalogStep rechecks catalog preconditions and marks the running step succeeded.
	SucceedValidateCatalogStep(context.Context, publish.SucceedStepParams) (publish.Step, error)

	// SucceedIncusIndexStep replaces Incus projection rows and marks the running step succeeded.
	SucceedIncusIndexStep(context.Context, publish.SucceedIncusIndexStepParams) (publish.Step, error)

	// FinalizePublishStep marks the version and job published after prior blocking steps succeed.
	FinalizePublishStep(context.Context, publish.SucceedStepParams) (publish.Job, error)

	// FailStep records a blocking step failure and marks the parent job failed.
	FailStep(context.Context, publish.FailStepParams) (publish.Step, error)
}

// IncusIndexer computes Incus projection rows for one version.
type IncusIndexer interface {
	// IndexVersion computes deterministic Incus projection rows for one frozen version.
	IndexVersion(context.Context, incus.IndexVersionParams) ([]incus.ProjectionRow, error)
}

// Config configures a durable publish worker.
type Config struct {
	// Store claims and completes durable publish steps.
	Store StepStore

	// Incus indexes eligible artifacts for Incus Simple Streams.
	Incus IncusIndexer

	// StaleStepTimeout controls when running steps can be reclaimed. Zero selects a conservative default.
	StaleStepTimeout time.Duration
}

// Job executes at most one publish step per RunOnce call.
type Job struct {
	store            StepStore
	incus            IncusIndexer
	staleStepTimeout time.Duration
}

// New constructs a publish worker job from config.
func New(config Config) *Job {
	staleStepTimeout := config.StaleStepTimeout
	if staleStepTimeout <= 0 {
		staleStepTimeout = defaultStaleStepTimeout
	}

	return &Job{
		store:            config.Store,
		incus:            config.Incus,
		staleStepTimeout: staleStepTimeout,
	}
}

// RunOnce claims and executes at most one queued or stale publish step.
func (job *Job) RunOnce(ctx context.Context, workerID string) (jobs.Result, error) {
	store, incusIndexer, staleStepTimeout, err := job.dependencies()
	if err != nil {
		return jobs.Result{}, err
	}

	step, err := store.ClaimStep(ctx, publish.ClaimStepParams{
		WorkerID:           workerID,
		StaleRunningBefore: time.Now().Add(-staleStepTimeout),
	})
	if err != nil {
		if errors.Is(err, publish.ErrNotFound) {
			return jobs.Result{}, nil
		}

		return jobs.Result{}, err
	}

	if err := job.runStep(ctx, store, incusIndexer, step, workerID); err != nil {
		if errors.Is(err, publish.ErrLeaseLost) {
			return jobs.Result{Worked: true}, nil
		}
		if _, failErr := store.FailStep(ctx, publish.FailStepParams{
			ID:             step.ID,
			WorkerID:       workerID,
			AttemptCount:   step.AttemptCount,
			FailureMessage: err.Error(),
		}); failErr != nil {
			if errors.Is(failErr, publish.ErrLeaseLost) {
				return jobs.Result{Worked: true}, nil
			}
			return jobs.Result{}, errors.Join(err, failErr)
		}
	}

	return jobs.Result{Worked: true}, nil
}

func (job *Job) runStep(
	ctx context.Context,
	store StepStore,
	incusIndexer IncusIndexer,
	step publish.Step,
	workerID string,
) error {
	switch step.Name {
	case publish.StepValidateCatalog:
		_, err := store.SucceedValidateCatalogStep(ctx, publish.SucceedStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
		})
		return err
	case publish.StepIncusIndex:
		rows, err := incusIndexer.IndexVersion(ctx, incus.IndexVersionParams{
			VersionID: step.VersionID,
			ImageName: step.ImageName,
			Version:   step.Version,
		})
		if err != nil {
			return err
		}
		_, err = store.SucceedIncusIndexStep(ctx, publish.SucceedIncusIndexStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
			VersionID:    step.VersionID,
			Rows:         rows,
		})
		return err
	case publish.StepFinalizePublish:
		_, err := store.FinalizePublishStep(ctx, publish.SucceedStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
		})
		return err
	default:
		return fmt.Errorf("%w: unknown publish step %q", publish.ErrFailedPrecondition, step.Name)
	}
}

func (job *Job) dependencies() (StepStore, IncusIndexer, time.Duration, error) {
	if job == nil {
		return nil, nil, 0, errors.New("publish job is not configured")
	}
	if job.store == nil {
		return nil, nil, 0, errors.New("publish store is required")
	}
	if job.incus == nil {
		return nil, nil, 0, errors.New("incus indexer is required")
	}

	return job.store, job.incus, job.staleStepTimeout, nil
}
