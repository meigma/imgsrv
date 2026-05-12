// Package publish implements the Postgres durable publish workflow adapter.
package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/materialization/incus"
	domain "github.com/meigma/imgsrv/internal/publish"
)

const (
	sqlStateCheckViolation      = "23514"
	sqlStateForeignKeyViolation = "23503"
	sqlStateInvalidText         = "22P02"
	sqlStateNotNullViolation    = "23502"
	sqlStateUniqueViolation     = "23505"

	jobColumns = `publish_jobs.id,
		publish_jobs.version_id,
		images.name,
		image_versions.version,
		publish_jobs.state,
		publish_jobs.started_at,
		publish_jobs.finished_at,
		publish_jobs.failure_message,
		publish_jobs.created_at,
		publish_jobs.updated_at`

	stepColumns = `publish_job_steps.id,
		publish_job_steps.job_id,
		publish_jobs.version_id,
		images.name,
		image_versions.version,
		publish_job_steps.name,
		publish_job_steps.state,
		publish_job_steps.blocking,
		publish_job_steps.sequence,
		publish_job_steps.attempt_count,
		publish_job_steps.run_after,
		publish_job_steps.locked_by,
		publish_job_steps.locked_at,
		publish_job_steps.started_at,
		publish_job_steps.finished_at,
		publish_job_steps.failure_message,
		publish_job_steps.created_at,
		publish_job_steps.updated_at`

	validateCatalogStepSequence = 10
	incusIndexStepSequence      = 20
	finalizePublishStepSequence = 30
)

// Store persists publish jobs, publish steps, and Incus projection rows in Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnqueueVersion freezes a draft version and creates its publish job.
func (store *Store) EnqueueVersion(ctx context.Context, params domain.EnqueueVersionParams) (domain.Job, error) {
	if err := domain.ValidateEnqueueVersionParams(params); err != nil {
		return domain.Job{}, err
	}

	var job domain.Job
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		versionID, state, err := lockVersionByRef(ctx, tx, params)
		if err != nil {
			return err
		}
		if state != catalog.VersionStateDraft {
			return fmt.Errorf("%w: version is not draft", domain.ErrFailedPrecondition)
		}
		if err := requirePublishableVersion(ctx, tx, versionID); err != nil {
			return err
		}
		if err := markVersionPublishing(ctx, tx, versionID); err != nil {
			return err
		}

		jobID := uuid.New()
		if _, err := tx.Exec(
			ctx,
			`INSERT INTO publish_jobs (id, version_id, state)
			VALUES ($1, $2, 'queued')`,
			jobID,
			versionID,
		); err != nil {
			return err
		}
		for _, step := range []struct {
			name     string
			sequence int
		}{
			{name: domain.StepValidateCatalog, sequence: validateCatalogStepSequence},
			{name: domain.StepIncusIndex, sequence: incusIndexStepSequence},
			{name: domain.StepFinalizePublish, sequence: finalizePublishStepSequence},
		} {
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO publish_job_steps (id, job_id, name, state, blocking, sequence)
				VALUES ($1, $2, $3, 'queued', true, $4)`,
				uuid.New(),
				jobID,
				step.name,
				step.sequence,
			); err != nil {
				return err
			}
		}

		var scanErr error
		job, scanErr = getJobByID(ctx, tx, jobID)
		return scanErr
	})
	if err != nil {
		return domain.Job{}, mapPublishError(err)
	}

	return job, nil
}

// GetJob returns a publish job with its ordered steps.
func (store *Store) GetJob(ctx context.Context, params domain.GetJobParams) (domain.Job, error) {
	if err := domain.ValidateGetJobParams(params); err != nil {
		return domain.Job{}, err
	}

	db, err := store.db()
	if err != nil {
		return domain.Job{}, err
	}

	job, err := getJobByID(ctx, db, params.ID)
	if err != nil {
		return domain.Job{}, mapPublishError(err)
	}

	return job, nil
}

// RetryJob requeues a failed publish job from its first failed blocking step.
func (store *Store) RetryJob(ctx context.Context, params domain.RetryJobParams) (domain.Job, error) {
	if err := domain.ValidateRetryJobParams(params); err != nil {
		return domain.Job{}, err
	}

	var job domain.Job
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		if err := lockJobForRetry(ctx, tx, params.ID); err != nil {
			return err
		}
		failedSequence, err := firstFailedBlockingStepSequence(ctx, tx, params.ID)
		if err != nil {
			return err
		}
		if err := markJobQueuedForRetry(ctx, tx, params.ID); err != nil {
			return err
		}
		if err := requeueStepsForRetry(ctx, tx, params.ID, failedSequence); err != nil {
			return err
		}

		var scanErr error
		job, scanErr = getJobByID(ctx, tx, params.ID)
		return scanErr
	})
	if err != nil {
		return domain.Job{}, mapPublishError(err)
	}

	return job, nil
}

// ClaimStep claims the next runnable publish step for a worker.
func (store *Store) ClaimStep(ctx context.Context, params domain.ClaimStepParams) (domain.Step, error) {
	if err := domain.ValidateClaimStepParams(params); err != nil {
		return domain.Step{}, err
	}

	var step domain.Step
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		step, err = claimStep(ctx, tx, params)
		if err != nil {
			return err
		}

		return markJobRunning(ctx, tx, step.JobID)
	})
	if err != nil {
		return domain.Step{}, mapPublishError(err)
	}

	return step, nil
}

// SucceedValidateCatalogStep rechecks catalog preconditions and marks the running step succeeded.
func (store *Store) SucceedValidateCatalogStep(
	ctx context.Context,
	params domain.SucceedStepParams,
) (domain.Step, error) {
	if err := domain.ValidateSucceedStepParams(params); err != nil {
		return domain.Step{}, err
	}

	var step domain.Step
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		step, err = lockRunningStep(ctx, tx, params.ID, params.WorkerID, params.AttemptCount)
		if err != nil {
			return err
		}
		if stepErr := requireStepName(step, domain.StepValidateCatalog); stepErr != nil {
			return stepErr
		}
		if stepErr := requirePublishableVersion(ctx, tx, step.VersionID); stepErr != nil {
			return stepErr
		}

		step, err = markStepSucceeded(ctx, tx, step.ID)
		return err
	})
	if err != nil {
		return domain.Step{}, mapPublishError(err)
	}

	return step, nil
}

// SucceedIncusIndexStep replaces Incus projection rows and marks the running step succeeded.
func (store *Store) SucceedIncusIndexStep(
	ctx context.Context,
	params domain.SucceedIncusIndexStepParams,
) (domain.Step, error) {
	if err := domain.ValidateSucceedIncusIndexStepParams(params); err != nil {
		return domain.Step{}, err
	}

	var step domain.Step
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		step, err = lockRunningStep(ctx, tx, params.ID, params.WorkerID, params.AttemptCount)
		if err != nil {
			return err
		}
		if stepErr := requireStepName(step, domain.StepIncusIndex); stepErr != nil {
			return stepErr
		}
		if step.VersionID != params.VersionID {
			return fmt.Errorf("%w: incus index version does not match step version", domain.ErrInvalid)
		}
		if stepErr := replaceIncusRows(ctx, tx, params.VersionID, params.Rows); stepErr != nil {
			return stepErr
		}

		step, err = markStepSucceeded(ctx, tx, step.ID)
		return err
	})
	if err != nil {
		return domain.Step{}, mapPublishError(err)
	}

	return step, nil
}

// FinalizePublishStep marks the version and job published after prior blocking steps succeed.
func (store *Store) FinalizePublishStep(
	ctx context.Context,
	params domain.SucceedStepParams,
) (domain.Job, error) {
	if err := domain.ValidateSucceedStepParams(params); err != nil {
		return domain.Job{}, err
	}

	var job domain.Job
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		step, err := lockRunningStep(ctx, tx, params.ID, params.WorkerID, params.AttemptCount)
		if err != nil {
			return err
		}
		if err := requireStepName(step, domain.StepFinalizePublish); err != nil {
			return err
		}
		if err := requirePreviousBlockingStepsSucceeded(ctx, tx, step); err != nil {
			return err
		}
		if err := markVersionPublished(ctx, tx, step.VersionID); err != nil {
			return err
		}
		if _, err := markStepSucceeded(ctx, tx, step.ID); err != nil {
			return err
		}
		if err := markJobSucceeded(ctx, tx, step.JobID); err != nil {
			return err
		}

		var scanErr error
		job, scanErr = getJobByID(ctx, tx, step.JobID)
		return scanErr
	})
	if err != nil {
		return domain.Job{}, mapPublishError(err)
	}

	return job, nil
}

// FailStep records a blocking step failure and marks the parent job failed.
func (store *Store) FailStep(ctx context.Context, params domain.FailStepParams) (domain.Step, error) {
	if err := domain.ValidateFailStepParams(params); err != nil {
		return domain.Step{}, err
	}

	var step domain.Step
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		step, err = lockRunningStep(ctx, tx, params.ID, params.WorkerID, params.AttemptCount)
		if err != nil {
			return err
		}
		step, err = markStepFailed(ctx, tx, step.ID, params.FailureMessage)
		if err != nil {
			return err
		}

		return markJobFailed(ctx, tx, step.JobID, params.FailureMessage)
	})
	if err != nil {
		return domain.Step{}, mapPublishError(err)
	}

	return step, nil
}

// ListProjectionRows returns completed projection rows for published versions.
func (store *Store) ListProjectionRows(ctx context.Context) ([]incus.ProjectionRow, error) {
	db, err := store.db()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		ctx,
		`SELECT incus_projection_items.version_id,
			incus_projection_items.artifact_id,
			incus_projection_items.metadata_attachment_id,
			images.name,
			images.display_name,
			image_versions.version,
			image_versions.created_at,
			image_versions.published_at,
			release_artifacts.operating_system,
			release_artifacts.architecture,
			incus_projection_items.metadata_path,
			incus_projection_items.disk_path,
			incus_projection_items.metadata_sha256,
			incus_projection_items.metadata_size_bytes,
			incus_projection_items.disk_sha256,
			incus_projection_items.disk_size_bytes,
			incus_projection_items.combined_disk_kvm_img_sha256
		FROM incus_projection_items
		INNER JOIN image_versions
			ON image_versions.id = incus_projection_items.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		INNER JOIN release_artifacts
			ON release_artifacts.id = incus_projection_items.artifact_id
		WHERE image_versions.state = 'published'
		ORDER BY images.name, image_versions.version, release_artifacts.architecture, release_artifacts.id`,
	)
	if err != nil {
		return nil, mapPublishError(err)
	}
	defer rows.Close()

	projectionRows := []incus.ProjectionRow{}
	for rows.Next() {
		row, err := scanProjectionRow(rows)
		if err != nil {
			return nil, mapPublishError(err)
		}
		projectionRows = append(projectionRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPublishError(err)
	}

	return projectionRows, nil
}

func lockVersionByRef(
	ctx context.Context,
	tx pgx.Tx,
	params domain.EnqueueVersionParams,
) (uuid.UUID, catalog.VersionState, error) {
	var versionID uuid.UUID
	var state catalog.VersionState
	err := tx.QueryRow(
		ctx,
		`SELECT image_versions.id, image_versions.state
		FROM image_versions
		INNER JOIN images ON images.id = image_versions.image_id
		WHERE images.name = $1
			AND image_versions.version = $2
		FOR UPDATE OF image_versions`,
		params.ImageName,
		params.Version,
	).Scan(&versionID, &state)
	if err != nil {
		return uuid.Nil, "", err
	}

	return versionID, state, nil
}

func lockJobForRetry(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) error {
	var jobState string
	var versionState string
	if err := tx.QueryRow(
		ctx,
		`SELECT publish_jobs.state, image_versions.state
		FROM publish_jobs
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		WHERE publish_jobs.id = $1
		FOR UPDATE OF publish_jobs, image_versions`,
		jobID,
	).Scan(&jobState, &versionState); err != nil {
		return err
	}
	if domain.JobState(jobState) != domain.JobStateFailed {
		return fmt.Errorf("%w: publish job is not failed", domain.ErrFailedPrecondition)
	}
	if catalog.VersionState(versionState) != catalog.VersionStatePublishing {
		return fmt.Errorf("%w: publish version is not publishing", domain.ErrFailedPrecondition)
	}

	return nil
}

func firstFailedBlockingStepSequence(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) (int, error) {
	var sequence int
	if err := tx.QueryRow(
		ctx,
		`SELECT sequence
		FROM publish_job_steps
		WHERE job_id = $1
			AND blocking
			AND state = 'failed'
		ORDER BY sequence
		FOR UPDATE OF publish_job_steps
		LIMIT 1`,
		jobID,
	).Scan(&sequence); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: publish job has no failed blocking step", domain.ErrFailedPrecondition)
		}

		return 0, err
	}

	return sequence, nil
}

func markJobQueuedForRetry(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE publish_jobs
		SET state = 'queued',
			finished_at = NULL,
			failure_message = NULL,
			updated_at = now()
		WHERE id = $1
			AND state = 'failed'`,
		jobID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: publish job is not failed", domain.ErrFailedPrecondition)
	}

	return nil
}

func requeueStepsForRetry(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, failedSequence int) error {
	_, err := tx.Exec(
		ctx,
		`UPDATE publish_job_steps
		SET state = 'queued',
			run_after = now(),
			locked_by = NULL,
			locked_at = NULL,
			finished_at = NULL,
			failure_message = NULL,
			updated_at = now()
		WHERE job_id = $1
			AND sequence >= $2
			AND state <> 'succeeded'`,
		jobID,
		failedSequence,
	)

	return err
}

func requirePublishableVersion(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) error {
	var failure string
	if err := tx.QueryRow(
		ctx,
		`SELECT CASE
			WHEN NOT EXISTS (
				SELECT 1
				FROM release_artifacts
				WHERE version_id = $1
			) THEN 'published image_versions require at least one release_artifact'
			WHEN EXISTS (
				SELECT 1
				FROM release_artifacts AS artifact
				LEFT JOIN cas_blobs AS blob
					ON blob.digest = artifact.primary_blob_digest
						AND blob.size_bytes = artifact.primary_blob_size_bytes
				WHERE artifact.version_id = $1
					AND blob.digest IS NULL
			) THEN 'published image_versions require verified primary blobs'
			WHEN EXISTS (
				SELECT 1
				FROM artifact_attachments AS attachment
				INNER JOIN release_artifacts AS artifact
					ON artifact.id = attachment.artifact_id
				LEFT JOIN cas_blobs AS blob
					ON blob.digest = attachment.blob_digest
						AND blob.size_bytes = attachment.blob_size_bytes
				WHERE artifact.version_id = $1
					AND blob.digest IS NULL
			) THEN 'published image_versions require verified attachment blobs'
			ELSE ''
		END`,
		versionID,
	).Scan(&failure); err != nil {
		return err
	}
	if failure != "" {
		return fmt.Errorf("%w: %s", domain.ErrFailedPrecondition, failure)
	}

	return nil
}

func markVersionPublishing(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE image_versions
		SET state = 'publishing',
			updated_at = now()
		WHERE id = $1
			AND state = 'draft'`,
		versionID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: version is not draft", domain.ErrFailedPrecondition)
	}

	return nil
}

func claimStep(ctx context.Context, tx pgx.Tx, params domain.ClaimStepParams) (domain.Step, error) {
	return scanStep(tx.QueryRow(
		ctx,
		`WITH next_step AS (
			SELECT publish_job_steps.id
			FROM publish_job_steps
			INNER JOIN publish_jobs
				ON publish_jobs.id = publish_job_steps.job_id
			WHERE publish_jobs.state IN ('queued', 'running')
				AND (
					(publish_job_steps.state = 'queued' AND publish_job_steps.run_after <= now())
					OR (publish_job_steps.state = 'running' AND publish_job_steps.locked_at <= $2)
				)
				AND NOT EXISTS (
					SELECT 1
					FROM publish_job_steps AS previous
					WHERE previous.job_id = publish_job_steps.job_id
						AND previous.sequence < publish_job_steps.sequence
						AND previous.blocking
						AND previous.state <> 'succeeded'
				)
			ORDER BY publish_job_steps.run_after, publish_job_steps.sequence, publish_job_steps.id
			FOR UPDATE OF publish_job_steps SKIP LOCKED
			LIMIT 1
		),
		updated_step AS (
			UPDATE publish_job_steps
			SET state = 'running',
				attempt_count = attempt_count + 1,
				locked_by = $1,
				locked_at = now(),
				started_at = COALESCE(started_at, now()),
				updated_at = now()
			WHERE id = (SELECT id FROM next_step)
			RETURNING *
		)
		SELECT `+stepColumns+`
		FROM updated_step AS publish_job_steps
		INNER JOIN publish_jobs
			ON publish_jobs.id = publish_job_steps.job_id
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		`,
		params.WorkerID,
		params.StaleRunningBefore,
	))
}

func markJobRunning(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) error {
	_, err := tx.Exec(
		ctx,
		`UPDATE publish_jobs
		SET state = 'running',
			started_at = COALESCE(started_at, now()),
			updated_at = now()
		WHERE id = $1
			AND state = 'queued'`,
		jobID,
	)

	return err
}

func lockRunningStep(
	ctx context.Context,
	tx pgx.Tx,
	stepID uuid.UUID,
	workerID string,
	attemptCount int,
) (domain.Step, error) {
	step, err := scanStep(tx.QueryRow(
		ctx,
		`SELECT `+stepColumns+`
		FROM publish_job_steps
		INNER JOIN publish_jobs
			ON publish_jobs.id = publish_job_steps.job_id
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		WHERE publish_job_steps.id = $1
			AND publish_job_steps.state = 'running'
			AND publish_job_steps.locked_by = $2
			AND publish_job_steps.attempt_count = $3
		FOR UPDATE OF publish_job_steps`,
		stepID,
		workerID,
		attemptCount,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Step{}, fmt.Errorf("%w: publish step lease is no longer owned", domain.ErrLeaseLost)
		}
		return domain.Step{}, err
	}

	return step, nil
}

func requireStepName(step domain.Step, want string) error {
	if step.Name != want {
		return fmt.Errorf("%w: publish step is %q, not %q", domain.ErrFailedPrecondition, step.Name, want)
	}

	return nil
}

func markStepSucceeded(ctx context.Context, tx pgx.Tx, stepID uuid.UUID) (domain.Step, error) {
	return scanStep(tx.QueryRow(
		ctx,
		`WITH updated_step AS (
			UPDATE publish_job_steps
			SET state = 'succeeded',
				locked_by = NULL,
				locked_at = NULL,
				finished_at = now(),
				updated_at = now()
			WHERE id = $1
			RETURNING *
		)
		SELECT `+stepColumns+`
		FROM updated_step AS publish_job_steps
		INNER JOIN publish_jobs
			ON publish_jobs.id = publish_job_steps.job_id
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		`,
		stepID,
	))
}

func markStepFailed(
	ctx context.Context,
	tx pgx.Tx,
	stepID uuid.UUID,
	failureMessage string,
) (domain.Step, error) {
	return scanStep(tx.QueryRow(
		ctx,
		`WITH updated_step AS (
			UPDATE publish_job_steps
			SET state = 'failed',
				locked_by = NULL,
				locked_at = NULL,
				finished_at = now(),
				failure_message = $2,
				updated_at = now()
			WHERE id = $1
			RETURNING *
		)
		SELECT `+stepColumns+`
		FROM updated_step AS publish_job_steps
		INNER JOIN publish_jobs
			ON publish_jobs.id = publish_job_steps.job_id
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		`,
		stepID,
		strings.TrimSpace(failureMessage),
	))
}

func requirePreviousBlockingStepsSucceeded(ctx context.Context, tx pgx.Tx, step domain.Step) error {
	var exists bool
	if err := tx.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM publish_job_steps
			WHERE job_id = $1
				AND sequence < $2
				AND blocking
				AND state <> 'succeeded'
		)`,
		step.JobID,
		step.Sequence,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: previous blocking publish steps have not succeeded", domain.ErrFailedPrecondition)
	}

	return nil
}

func markVersionPublished(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE image_versions
		SET state = 'published',
			published_at = now(),
			updated_at = now()
		WHERE id = $1
			AND state = 'publishing'`,
		versionID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: version is not publishing", domain.ErrFailedPrecondition)
	}

	return nil
}

func markJobSucceeded(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE publish_jobs
		SET state = 'succeeded',
			finished_at = now(),
			updated_at = now()
		WHERE id = $1
			AND state = 'running'`,
		jobID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: publish job is not running", domain.ErrFailedPrecondition)
	}

	return nil
}

func markJobFailed(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, failureMessage string) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE publish_jobs
		SET state = 'failed',
			finished_at = now(),
			failure_message = $2,
			updated_at = now()
		WHERE id = $1
			AND state IN ('queued', 'running')`,
		jobID,
		strings.TrimSpace(failureMessage),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: publish job is already terminal", domain.ErrFailedPrecondition)
	}

	return nil
}

func replaceIncusRows(
	ctx context.Context,
	tx pgx.Tx,
	versionID uuid.UUID,
	rows []incus.ProjectionRow,
) error {
	if _, err := tx.Exec(
		ctx,
		`DELETE FROM incus_projection_items
		WHERE version_id = $1`,
		versionID,
	); err != nil {
		return err
	}
	for _, row := range rows {
		if row.VersionID != versionID {
			return fmt.Errorf("%w: incus projection row version does not match publish version", domain.ErrInvalid)
		}
		if _, err := tx.Exec(
			ctx,
			`INSERT INTO incus_projection_items (
				artifact_id,
				version_id,
				metadata_attachment_id,
				metadata_path,
				disk_path,
				metadata_sha256,
				metadata_size_bytes,
				disk_sha256,
				disk_size_bytes,
				combined_disk_kvm_img_sha256
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			row.ArtifactID,
			row.VersionID,
			row.MetadataAttachmentID,
			row.MetadataPath,
			row.DiskPath,
			row.MetadataSHA256,
			row.MetadataSizeBytes,
			row.DiskSHA256,
			row.DiskSizeBytes,
			row.CombinedDiskKVMImgSHA256,
		); err != nil {
			return err
		}
	}

	return nil
}

func getJobByID(ctx context.Context, db queryer, jobID uuid.UUID) (domain.Job, error) {
	job, err := scanJob(db.QueryRow(
		ctx,
		`SELECT `+jobColumns+`
		FROM publish_jobs
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		WHERE publish_jobs.id = $1`,
		jobID,
	))
	if err != nil {
		return domain.Job{}, err
	}

	rows, err := db.Query(
		ctx,
		`SELECT `+stepColumns+`
		FROM publish_job_steps
		INNER JOIN publish_jobs
			ON publish_jobs.id = publish_job_steps.job_id
		INNER JOIN image_versions
			ON image_versions.id = publish_jobs.version_id
		INNER JOIN images
			ON images.id = image_versions.image_id
		WHERE publish_job_steps.job_id = $1
		ORDER BY publish_job_steps.sequence`,
		jobID,
	)
	if err != nil {
		return domain.Job{}, err
	}
	defer rows.Close()

	for rows.Next() {
		step, err := scanStep(rows)
		if err != nil {
			return domain.Job{}, err
		}
		job.Steps = append(job.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return domain.Job{}, err
	}

	return job, nil
}

func scanJob(row pgx.Row) (domain.Job, error) {
	var job domain.Job
	var state string
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	var failureMessage sql.NullString

	err := row.Scan(
		&job.ID,
		&job.VersionID,
		&job.ImageName,
		&job.Version,
		&state,
		&startedAt,
		&finishedAt,
		&failureMessage,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		return domain.Job{}, err
	}
	job.State = domain.JobState(state)
	job.StartedAt = optionalTime(startedAt)
	job.FinishedAt = optionalTime(finishedAt)
	job.FailureMessage = optionalString(failureMessage)

	return job, nil
}

func scanStep(row pgx.Row) (domain.Step, error) {
	var step domain.Step
	var state string
	var lockedBy sql.NullString
	var lockedAt sql.NullTime
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	var failureMessage sql.NullString

	err := row.Scan(
		&step.ID,
		&step.JobID,
		&step.VersionID,
		&step.ImageName,
		&step.Version,
		&step.Name,
		&state,
		&step.Blocking,
		&step.Sequence,
		&step.AttemptCount,
		&step.RunAfter,
		&lockedBy,
		&lockedAt,
		&startedAt,
		&finishedAt,
		&failureMessage,
		&step.CreatedAt,
		&step.UpdatedAt,
	)
	if err != nil {
		return domain.Step{}, err
	}
	step.State = domain.StepState(state)
	step.LockedBy = optionalString(lockedBy)
	step.LockedAt = optionalTime(lockedAt)
	step.StartedAt = optionalTime(startedAt)
	step.FinishedAt = optionalTime(finishedAt)
	step.FailureMessage = optionalString(failureMessage)

	return step, nil
}

func scanProjectionRow(row pgx.Row) (incus.ProjectionRow, error) {
	var projection incus.ProjectionRow
	var displayName sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&projection.VersionID,
		&projection.ArtifactID,
		&projection.MetadataAttachmentID,
		&projection.ImageName,
		&displayName,
		&projection.Version,
		&projection.VersionCreatedAt,
		&publishedAt,
		&projection.OperatingSystem,
		&projection.Architecture,
		&projection.MetadataPath,
		&projection.DiskPath,
		&projection.MetadataSHA256,
		&projection.MetadataSizeBytes,
		&projection.DiskSHA256,
		&projection.DiskSizeBytes,
		&projection.CombinedDiskKVMImgSHA256,
	)
	if err != nil {
		return incus.ProjectionRow{}, err
	}
	projection.ImageDisplayName = optionalString(displayName)
	projection.PublishedAt = optionalTime(publishedAt)

	return projection, nil
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (store *Store) db() (*pgxpool.Pool, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("postgres publish store is not open")
	}

	return store.pool, nil
}

func (store *Store) withTx(ctx context.Context, apply func(pgx.Tx) error) error {
	db, err := store.db()
	if err != nil {
		return err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := apply(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit postgres transaction: %w", err)
	}

	return nil
}

func mapPublishError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		return fmt.Errorf("%w: %s", domain.ErrFailedPrecondition, pgErr.ConstraintName)
	case sqlStateForeignKeyViolation:
		return fmt.Errorf("%w: %s", domain.ErrNotFound, pgErr.Message)
	case sqlStateCheckViolation:
		return fmt.Errorf("%w: %s", domain.ErrFailedPrecondition, pgErr.Message)
	case sqlStateInvalidText, sqlStateNotNullViolation:
		return fmt.Errorf("%w: %s", domain.ErrInvalid, pgErr.Message)
	default:
		return err
	}
}
