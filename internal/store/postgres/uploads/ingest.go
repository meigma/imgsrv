package uploads

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

// ClaimIngestJob claims the next queued CAS ingest job.
func (store *Store) ClaimIngestJob(ctx context.Context, params domain.ClaimIngestJobParams) (domain.IngestJob, error) {
	if err := validateClaimIngestJobParams(params); err != nil {
		return domain.IngestJob{}, err
	}

	var job domain.IngestJob
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		job, err = claimIngestJob(ctx, tx, params.WorkerID)
		if err != nil {
			return err
		}

		return markSessionIngesting(ctx, tx, job.UploadID)
	})
	if err != nil {
		return domain.IngestJob{}, mapUploadError(err)
	}

	return job, nil
}

// SucceedIngestJob records successful CAS ingest.
func (store *Store) SucceedIngestJob(
	ctx context.Context,
	params domain.SucceedIngestJobParams,
) (domain.IngestJob, error) {
	if err := validateSucceedIngestJobParams(params); err != nil {
		return domain.IngestJob{}, err
	}

	var job domain.IngestJob
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		job, err = lockRunningJob(ctx, tx, params.ID)
		if err != nil {
			return err
		}
		if casErr := ensureCASBlob(ctx, tx, params); casErr != nil {
			return casErr
		}
		if readyErr := markSessionReady(ctx, tx, job.UploadID, params); readyErr != nil {
			return readyErr
		}

		job, err = markJobSucceeded(ctx, tx, params.ID, params.Digest)
		return err
	})
	if err != nil {
		return domain.IngestJob{}, mapUploadError(err)
	}

	return job, nil
}

// FailIngestJob records failed CAS ingest.
func (store *Store) FailIngestJob(ctx context.Context, params domain.FailIngestJobParams) (domain.IngestJob, error) {
	if err := validateFailIngestJobParams(params); err != nil {
		return domain.IngestJob{}, err
	}

	var job domain.IngestJob
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		job, err = lockRunningJob(ctx, tx, params.ID)
		if err != nil {
			return err
		}
		if failedErr := markSessionFailed(ctx, tx, job.UploadID, params.FailureMessage); failedErr != nil {
			return failedErr
		}

		job, err = markJobFailed(ctx, tx, params.ID, params.FailureMessage)
		return err
	})
	if err != nil {
		return domain.IngestJob{}, mapUploadError(err)
	}

	return job, nil
}

// claimIngestJob atomically picks the oldest queued ingest job whose RunAfter
// has elapsed, marks it running for workerID, and returns the updated row.
func claimIngestJob(ctx context.Context, tx pgx.Tx, workerID string) (domain.IngestJob, error) {
	return scanIngestJob(tx.QueryRow(
		ctx,
		`WITH next_job AS (
			SELECT id AS job_id
			FROM cas_ingest_jobs
			WHERE state = 'queued'
				AND run_after <= now()
			ORDER BY run_after, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE cas_ingest_jobs
		SET state = 'running',
			attempt_count = attempt_count + 1,
			locked_by = $1,
			locked_at = now(),
			started_at = COALESCE(started_at, now()),
			updated_at = now()
		FROM next_job
		WHERE cas_ingest_jobs.id = next_job.job_id
		RETURNING `+ingestJobColumns,
		workerID,
	))
}

// lockRunningJob row-locks the ingest job identified by jobID and returns it
// only when the durable state is running.
func lockRunningJob(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) (domain.IngestJob, error) {
	job, err := scanIngestJob(tx.QueryRow(
		ctx,
		`SELECT `+ingestJobColumns+`
		FROM cas_ingest_jobs
		WHERE id = $1
		FOR UPDATE`,
		jobID,
	))
	if err != nil {
		return domain.IngestJob{}, err
	}
	if job.State != domain.IngestJobStateRunning {
		return domain.IngestJob{}, fmt.Errorf("%w: ingest job is not running", domain.ErrFailedPrecondition)
	}

	return job, nil
}

// markSessionIngesting transitions an upload session from completed to
// ingesting and returns ErrFailedPrecondition when no row matches.
func markSessionIngesting(ctx context.Context, tx pgx.Tx, uploadID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE upload_sessions
		SET state = 'ingesting',
			ingest_started_at = COALESCE(ingest_started_at, now()),
			updated_at = now()
		WHERE id = $1
			AND state = 'completed'`,
		uploadID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: upload session is not ready for ingest", domain.ErrFailedPrecondition)
	}

	return nil
}

// ensureCASBlob inserts the verified CAS blob row for params, treating an
// existing record with the same digest as a no-op.
func ensureCASBlob(ctx context.Context, tx pgx.Tx, params domain.SucceedIngestJobParams) error {
	_, err := tx.Exec(
		ctx,
		`INSERT INTO cas_blobs (digest, size_bytes, storage_key, media_type, verified_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (digest) DO NOTHING`,
		params.Digest,
		params.SizeBytes,
		params.StorageKey,
		params.MediaType,
	)

	return err
}

// markSessionReady transitions an ingesting upload session to ready when its
// expected digest and size match the freshly verified CAS blob, and returns
// ErrFailedPrecondition otherwise.
func markSessionReady(
	ctx context.Context,
	tx pgx.Tx,
	uploadID uuid.UUID,
	params domain.SucceedIngestJobParams,
) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE upload_sessions
		SET state = 'ready',
			ready_at = now(),
			ready_blob_digest = $2,
			updated_at = now()
		WHERE id = $1
			AND state = 'ingesting'
			AND expected_digest = $2
			AND expected_size_bytes = $3
			AND EXISTS (
				SELECT 1
				FROM cas_blobs
				WHERE digest = $2
					AND size_bytes = $3
			)`,
		uploadID,
		params.Digest,
		params.SizeBytes,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: upload session does not match verified blob", domain.ErrFailedPrecondition)
	}

	return nil
}

// markSessionFailed transitions an ingesting upload session to failed and
// records failureMessage, returning ErrFailedPrecondition when no row matches.
func markSessionFailed(ctx context.Context, tx pgx.Tx, uploadID uuid.UUID, failureMessage string) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE upload_sessions
		SET state = 'failed',
			failed_at = now(),
			failure_message = $2,
			updated_at = now()
		WHERE id = $1
			AND state = 'ingesting'`,
		uploadID,
		failureMessage,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: upload session is not ingesting", domain.ErrFailedPrecondition)
	}

	return nil
}

// markJobSucceeded transitions a running ingest job to succeeded, records
// the verified digest, clears worker locks, and returns the updated row.
func markJobSucceeded(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, digest domain.Digest) (domain.IngestJob, error) {
	return scanIngestJob(tx.QueryRow(
		ctx,
		`UPDATE cas_ingest_jobs
		SET state = 'succeeded',
			finished_at = now(),
			blob_digest = $2,
			locked_by = NULL,
			locked_at = NULL,
			updated_at = now()
		WHERE id = $1
			AND state = 'running'
		RETURNING `+ingestJobColumns,
		jobID,
		digest,
	))
}

// markJobFailed transitions a running ingest job to failed, records
// failureMessage, clears worker locks, and returns the updated row.
func markJobFailed(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, failureMessage string) (domain.IngestJob, error) {
	return scanIngestJob(tx.QueryRow(
		ctx,
		`UPDATE cas_ingest_jobs
		SET state = 'failed',
			finished_at = now(),
			failure_message = $2,
			locked_by = NULL,
			locked_at = NULL,
			updated_at = now()
		WHERE id = $1
			AND state = 'running'
		RETURNING `+ingestJobColumns,
		jobID,
		failureMessage,
	))
}
