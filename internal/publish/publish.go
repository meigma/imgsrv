// Package publish defines the durable image publish workflow boundary.
package publish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/materialization/incus"
)

// Error identifies a category of publish-workflow failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrFailedPrecondition means the publish workflow cannot proceed from the current state.
	ErrFailedPrecondition Error = "publish failed precondition"

	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "publish invalid input"

	// ErrLeaseLost means a worker tried to complete a step it no longer owns.
	ErrLeaseLost Error = "publish lease lost"

	// ErrNotFound means the requested publish resource does not exist.
	ErrNotFound Error = "publish not found"
)

// JobState is the lifecycle state of a durable publish job.
type JobState string

const (
	// JobStateQueued means no publish step has started yet.
	JobStateQueued JobState = "queued"

	// JobStateRunning means at least one publish step is running or has run.
	JobStateRunning JobState = "running"

	// JobStateSucceeded means all blocking publish work completed and the version is published.
	JobStateSucceeded JobState = "succeeded"

	// JobStateFailed means a blocking publish step failed.
	JobStateFailed JobState = "failed"
)

// StepState is the lifecycle state of one durable publish step.
type StepState string

const (
	// StepStateQueued means the step is waiting to be claimed.
	StepStateQueued StepState = "queued"

	// StepStateRunning means a worker has claimed the step.
	StepStateRunning StepState = "running"

	// StepStateSucceeded means the step completed successfully.
	StepStateSucceeded StepState = "succeeded"

	// StepStateFailed means the step failed and recorded an operator-visible reason.
	StepStateFailed StepState = "failed"

	// StepStateSkipped means the step was intentionally skipped.
	StepStateSkipped StepState = "skipped"
)

const (
	// StepValidateCatalog checks publish preconditions against durable catalog state.
	StepValidateCatalog = "validate_catalog"

	// StepIncusIndex computes and persists Incus Simple Streams projection rows.
	StepIncusIndex = "incus_index"

	// StepFinalizePublish marks a successfully indexed publishing version as published.
	StepFinalizePublish = "finalize_publish"
)

// Job describes one durable publish workflow.
type Job struct {
	// ID is the stable publish-job identity.
	ID uuid.UUID

	// VersionID identifies the image version being published.
	VersionID uuid.UUID

	// ImageName is the image namespace for the version being published.
	ImageName string

	// Version is the operator-defined version string being published.
	Version string

	// State is the durable publish-job lifecycle state.
	State JobState

	// StartedAt is set when a worker first claims a step.
	StartedAt *time.Time

	// FinishedAt is set when the job reaches a terminal state.
	FinishedAt *time.Time

	// FailureMessage describes the blocking failure when State is failed.
	FailureMessage *string

	// CreatedAt is when the job was queued.
	CreatedAt time.Time

	// UpdatedAt is when the job last changed.
	UpdatedAt time.Time

	// Steps are the durable units of publish progress in execution order.
	Steps []Step
}

// Step describes one durable unit of publish progress.
type Step struct {
	// ID is the stable publish-step identity.
	ID uuid.UUID

	// JobID identifies the parent publish job.
	JobID uuid.UUID

	// VersionID identifies the image version being published.
	VersionID uuid.UUID

	// ImageName is the image namespace for the version being published.
	ImageName string

	// Version is the operator-defined version string being published.
	Version string

	// Name identifies the Go step handler to execute.
	Name string

	// State is the durable publish-step lifecycle state.
	State StepState

	// Blocking controls whether failure blocks later publish steps.
	Blocking bool

	// Sequence orders steps within the parent job.
	Sequence int

	// AttemptCount counts durable claims of this step.
	AttemptCount int

	// RunAfter is the earliest time the step may be claimed.
	RunAfter time.Time

	// LockedBy identifies the worker that currently owns a running step.
	LockedBy *string

	// LockedAt is when the current worker lock was taken.
	LockedAt *time.Time

	// StartedAt is set when the step is first claimed.
	StartedAt *time.Time

	// FinishedAt is set when the step reaches a terminal state.
	FinishedAt *time.Time

	// FailureMessage describes the failure when State is failed.
	FailureMessage *string

	// CreatedAt is when the step was queued.
	CreatedAt time.Time

	// UpdatedAt is when the step last changed.
	UpdatedAt time.Time
}

// Store persists durable publish workflow state.
type Store interface {
	// EnqueueVersion freezes a draft version and creates its publish job.
	EnqueueVersion(context.Context, EnqueueVersionParams) (Job, error)

	// GetJob returns a publish job with its ordered steps.
	GetJob(context.Context, GetJobParams) (Job, error)

	// RetryJob requeues a failed publish job from its first failed blocking step.
	RetryJob(context.Context, RetryJobParams) (Job, error)

	// ClaimStep claims the next runnable publish step for a worker.
	ClaimStep(context.Context, ClaimStepParams) (Step, error)

	// SucceedValidateCatalogStep rechecks catalog preconditions and marks the running step succeeded.
	SucceedValidateCatalogStep(context.Context, SucceedStepParams) (Step, error)

	// SucceedIncusIndexStep replaces Incus projection rows and marks the running step succeeded.
	SucceedIncusIndexStep(context.Context, SucceedIncusIndexStepParams) (Step, error)

	// FinalizePublishStep marks the version and job published after prior blocking steps succeed.
	FinalizePublishStep(context.Context, SucceedStepParams) (Job, error)

	// FailStep records a blocking step failure and marks the parent job failed.
	FailStep(context.Context, FailStepParams) (Step, error)
}

// EnqueueVersionParams identifies the draft version to publish.
type EnqueueVersionParams struct {
	// ImageName is the operator-defined image namespace.
	ImageName string

	// Version is the operator-defined version string.
	Version string
}

// GetJobParams identifies a publish job.
type GetJobParams struct {
	// ID is the publish job ID.
	ID uuid.UUID
}

// RetryJobParams identifies a failed publish job to retry.
type RetryJobParams struct {
	// ID is the publish job ID.
	ID uuid.UUID
}

// ClaimStepParams configures durable step claiming.
type ClaimStepParams struct {
	// WorkerID identifies the process worker claiming work.
	WorkerID string

	// StaleRunningBefore is the lock timestamp cutoff for reclaiming running steps.
	StaleRunningBefore time.Time
}

// SucceedStepParams identifies a running step that completed successfully.
type SucceedStepParams struct {
	// ID is the publish step ID.
	ID uuid.UUID

	// WorkerID identifies the worker that claimed this attempt.
	WorkerID string

	// AttemptCount is the durable claim attempt being completed.
	AttemptCount int
}

// SucceedIncusIndexStepParams records Incus projection rows for a completed step.
type SucceedIncusIndexStepParams struct {
	// ID is the publish step ID.
	ID uuid.UUID

	// WorkerID identifies the worker that claimed this attempt.
	WorkerID string

	// AttemptCount is the durable claim attempt being completed.
	AttemptCount int

	// VersionID identifies the version whose Incus projection rows are replaced.
	VersionID uuid.UUID

	// Rows are the deterministic Incus projection rows computed by the step.
	Rows []incus.ProjectionRow
}

// FailStepParams identifies a running step that failed.
type FailStepParams struct {
	// ID is the publish step ID.
	ID uuid.UUID

	// WorkerID identifies the worker that claimed this attempt.
	WorkerID string

	// AttemptCount is the durable claim attempt being failed.
	AttemptCount int

	// FailureMessage is the operator-visible failure reason.
	FailureMessage string
}

// ValidateEnqueueVersionParams validates the inputs to Store.EnqueueVersion.
func ValidateEnqueueVersionParams(params EnqueueVersionParams) error {
	if err := catalog.ValidateImageName(params.ImageName); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := catalog.ValidateVersion(params.Version); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// ValidateGetJobParams validates the inputs to Store.GetJob.
func ValidateGetJobParams(params GetJobParams) error {
	if params.ID == uuid.Nil {
		return fmt.Errorf("%w: publish job id is required", ErrInvalid)
	}

	return nil
}

// ValidateRetryJobParams validates the inputs to Store.RetryJob.
func ValidateRetryJobParams(params RetryJobParams) error {
	if params.ID == uuid.Nil {
		return fmt.Errorf("%w: publish job id is required", ErrInvalid)
	}

	return nil
}

// ValidateClaimStepParams validates the inputs to Store.ClaimStep.
func ValidateClaimStepParams(params ClaimStepParams) error {
	if strings.TrimSpace(params.WorkerID) == "" {
		return fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	if params.StaleRunningBefore.IsZero() {
		return fmt.Errorf("%w: stale running cutoff is required", ErrInvalid)
	}

	return nil
}

// ValidateSucceedStepParams validates the inputs to successful step operations.
func ValidateSucceedStepParams(params SucceedStepParams) error {
	return validateStepLease(params.ID, params.WorkerID, params.AttemptCount)
}

// ValidateSucceedIncusIndexStepParams validates the inputs to Store.SucceedIncusIndexStep.
func ValidateSucceedIncusIndexStepParams(params SucceedIncusIndexStepParams) error {
	if err := ValidateSucceedStepParams(SucceedStepParams{
		ID:           params.ID,
		WorkerID:     params.WorkerID,
		AttemptCount: params.AttemptCount,
	}); err != nil {
		return err
	}
	if params.VersionID == uuid.Nil {
		return fmt.Errorf("%w: version id is required", ErrInvalid)
	}

	return nil
}

// ValidateFailStepParams validates the inputs to Store.FailStep.
func ValidateFailStepParams(params FailStepParams) error {
	if err := validateStepLease(params.ID, params.WorkerID, params.AttemptCount); err != nil {
		return err
	}
	if strings.TrimSpace(params.FailureMessage) == "" {
		return fmt.Errorf("%w: failure message is required", ErrInvalid)
	}

	return nil
}

func validateStepLease(stepID uuid.UUID, workerID string, attemptCount int) error {
	if stepID == uuid.Nil {
		return fmt.Errorf("%w: publish step id is required", ErrInvalid)
	}
	if strings.TrimSpace(workerID) == "" {
		return fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	if attemptCount <= 0 {
		return fmt.Errorf("%w: attempt count is required", ErrInvalid)
	}

	return nil
}
