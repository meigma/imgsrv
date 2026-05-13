// Package publishflow claims durable publish steps and executes their handlers.
package publishflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meigma/imgsrv/internal/jobs"
	safelog "github.com/meigma/imgsrv/internal/logging"
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

	// Logger receives publish worker logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Job executes at most one publish step per RunOnce call.
type Job struct {
	store            StepStore
	incus            IncusIndexer
	staleStepTimeout time.Duration
	logger           *slog.Logger
}

// New constructs a publish worker job from config.
func New(config Config) *Job {
	staleStepTimeout := config.StaleStepTimeout
	if staleStepTimeout <= 0 {
		staleStepTimeout = defaultStaleStepTimeout
	}
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	return &Job{
		store:            config.Store,
		incus:            config.Incus,
		staleStepTimeout: staleStepTimeout,
		logger:           logger,
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
	attrs := stepAttrs(step)
	job.logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"publish step claimed",
		append(attrs, slog.String("operation", "publish.claim_step"))...)

	stepResult, err := job.runStep(ctx, store, incusIndexer, step, workerID)
	if err != nil {
		if errors.Is(err, publish.ErrLeaseLost) {
			job.logger.LogAttrs(
				ctx,
				slog.LevelDebug,
				"publish step lease lost",
				append(attrs, slog.String("operation", "publish.lease_lost"))...)
			return jobs.Result{Worked: true, Attrs: attrs}, nil
		}
		if _, failErr := store.FailStep(ctx, publish.FailStepParams{
			ID:             step.ID,
			WorkerID:       workerID,
			AttemptCount:   step.AttemptCount,
			FailureMessage: err.Error(),
		}); failErr != nil {
			if errors.Is(failErr, publish.ErrLeaseLost) {
				job.logger.LogAttrs(
					ctx,
					slog.LevelDebug,
					"publish step failure lease lost",
					append(attrs, slog.String("operation", "publish.fail_lease_lost"))...)
				return jobs.Result{Worked: true, Attrs: attrs}, nil
			}
			return jobs.Result{Worked: true, Attrs: attrs}, errors.Join(err, failErr)
		}
		job.logger.LogAttrs(
			ctx,
			slog.LevelWarn,
			"publish step failed",
			append(attrs, slog.String("operation", "publish.fail_step"), slog.Any("error", err))...,
		)

		return jobs.Result{Worked: true, Attrs: attrs}, nil
	}
	if stepResult.projectionRows >= 0 {
		attrs = append(attrs, slog.Int("projection_rows", stepResult.projectionRows))
	}
	job.logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"publish step succeeded",
		append(attrs, slog.String("operation", "publish.succeed_step"))...)

	return jobs.Result{Worked: true, Attrs: attrs}, nil
}

type runStepResult struct {
	projectionRows int
}

func (job *Job) runStep(
	ctx context.Context,
	store StepStore,
	incusIndexer IncusIndexer,
	step publish.Step,
	workerID string,
) (runStepResult, error) {
	switch step.Name {
	case publish.StepValidateCatalog:
		_, err := store.SucceedValidateCatalogStep(ctx, publish.SucceedStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
		})
		return runStepResult{projectionRows: -1}, err
	case publish.StepIncusIndex:
		rows, err := incusIndexer.IndexVersion(ctx, incus.IndexVersionParams{
			VersionID: step.VersionID,
			ImageName: step.ImageName,
			Version:   step.Version,
		})
		if err != nil {
			return runStepResult{}, err
		}
		_, err = store.SucceedIncusIndexStep(ctx, publish.SucceedIncusIndexStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
			VersionID:    step.VersionID,
			Rows:         rows,
		})
		return runStepResult{projectionRows: len(rows)}, err
	case publish.StepFinalizePublish:
		_, err := store.FinalizePublishStep(ctx, publish.SucceedStepParams{
			ID:           step.ID,
			WorkerID:     workerID,
			AttemptCount: step.AttemptCount,
		})
		return runStepResult{projectionRows: -1}, err
	default:
		return runStepResult{}, fmt.Errorf("%w: unknown publish step %q", publish.ErrFailedPrecondition, step.Name)
	}
}

func stepAttrs(step publish.Step) []slog.Attr {
	return []slog.Attr{
		slog.String("publish_job_id", step.JobID.String()),
		slog.String("publish_step_id", step.ID.String()),
		slog.String("version_id", step.VersionID.String()),
		slog.String("image_name", step.ImageName),
		slog.String("version", step.Version),
		slog.String("step", step.Name),
		slog.Int("attempt_count", step.AttemptCount),
		slog.String("state", string(step.State)),
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
