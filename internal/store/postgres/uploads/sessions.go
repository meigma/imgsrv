package uploads

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

// CreateSession creates durable upload state after object-storage upload initiation.
func (store *Store) CreateSession(ctx context.Context, params domain.CreateSessionParams) (domain.Session, error) {
	if err := validateCreateSessionParams(params); err != nil {
		return domain.Session{}, err
	}

	db, err := store.uploadDB()
	if err != nil {
		return domain.Session{}, err
	}

	session, err := scanSession(db.QueryRow(
		ctx,
		`INSERT INTO upload_sessions (
			id,
			expected_digest,
			expected_size_bytes,
			state,
			storage_upload_id,
			staging_key,
			media_type_hint,
			filename_hint,
			expires_at
		)
		VALUES ($1, $2, $3, 'created', $4, $5, $6, $7, $8)
		RETURNING `+sessionColumns,
		params.ID,
		params.ExpectedDigest,
		params.ExpectedSizeBytes,
		params.StorageUploadID,
		domain.StagingKey(params.ID),
		params.MediaTypeHint,
		params.FilenameHint,
		params.ExpiresAt,
	))
	if err != nil {
		return domain.Session{}, mapUploadError(err)
	}

	return session, nil
}

// CreateReadySession creates a terminal ready upload session for a trusted CAS blob.
func (store *Store) CreateReadySession(
	ctx context.Context,
	params domain.CreateReadySessionParams,
) (domain.Session, error) {
	if err := validateCreateReadySessionParams(params); err != nil {
		return domain.Session{}, err
	}

	db, err := store.uploadDB()
	if err != nil {
		return domain.Session{}, err
	}

	session, err := scanSession(db.QueryRow(
		ctx,
		`INSERT INTO upload_sessions (
			id,
			expected_digest,
			expected_size_bytes,
			state,
			storage_upload_id,
			staging_key,
			media_type_hint,
			filename_hint,
			completed_at,
			ready_at,
			expires_at,
			ready_blob_digest
		)
		VALUES ($1, $2, $3, 'ready', $4, $5, $6, $7, now(), now(), $8, $2)
		RETURNING `+sessionColumns,
		params.ID,
		params.ExpectedDigest,
		params.ExpectedSizeBytes,
		readyStorageUploadID(params.ID),
		domain.StagingKey(params.ID),
		params.MediaTypeHint,
		params.FilenameHint,
		params.ExpiresAt,
	))
	if err != nil {
		return domain.Session{}, mapUploadError(err)
	}

	return session, nil
}

// GetSession looks up durable upload state.
func (store *Store) GetSession(ctx context.Context, params domain.GetSessionParams) (domain.Session, error) {
	if err := validateGetSessionParams(params); err != nil {
		return domain.Session{}, err
	}

	db, err := store.uploadDB()
	if err != nil {
		return domain.Session{}, err
	}

	session, err := scanSession(db.QueryRow(
		ctx,
		`SELECT `+sessionColumns+`
		FROM upload_sessions
		WHERE id = $1`,
		params.ID,
	))
	if err != nil {
		return domain.Session{}, mapUploadError(err)
	}

	return session, nil
}

// PutPart records or replaces an upload part.
func (store *Store) PutPart(ctx context.Context, params domain.PutPartParams) (domain.Part, error) {
	if err := validatePutPartParams(params); err != nil {
		return domain.Part{}, err
	}

	var part domain.Part
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		if err := requireMutableUploadSession(ctx, tx, params.UploadID); err != nil {
			return err
		}

		var scanErr error
		part, scanErr = scanPart(tx.QueryRow(
			ctx,
			`INSERT INTO upload_parts (upload_id, part_number, etag, size_bytes)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (upload_id, part_number)
			DO UPDATE SET etag = excluded.etag,
				size_bytes = excluded.size_bytes,
				updated_at = now()
			RETURNING `+partColumns,
			params.UploadID,
			params.PartNumber,
			params.ETag,
			params.SizeBytes,
		))

		return scanErr
	})
	if err != nil {
		return domain.Part{}, mapUploadError(err)
	}

	return part, nil
}

// CompleteSession marks object-storage multipart completion and queues CAS ingest.
func (store *Store) CompleteSession(
	ctx context.Context,
	params domain.CompleteSessionParams,
) (domain.Session, error) {
	if err := validateCompleteSessionParams(params); err != nil {
		return domain.Session{}, err
	}

	var session domain.Session
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		session, err = completeSession(ctx, tx, params.ID)
		return err
	})
	if err != nil {
		return domain.Session{}, mapUploadError(err)
	}

	return session, nil
}

// AbortSession aborts an upload before ingest starts.
func (store *Store) AbortSession(ctx context.Context, params domain.AbortSessionParams) (domain.Session, error) {
	if err := validateAbortSessionParams(params); err != nil {
		return domain.Session{}, err
	}

	var session domain.Session
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		session, err = abortSession(ctx, tx, params.ID)
		return err
	})
	if err != nil {
		return domain.Session{}, mapUploadError(err)
	}

	return session, nil
}

// requireMutableUploadSession transitions the upload session into uploading
// when it is still in created or uploading, and returns ErrFailedPrecondition
// when the session exists but no longer accepts new parts.
func requireMutableUploadSession(ctx context.Context, tx pgx.Tx, uploadID uuid.UUID) error {
	tag, err := tx.Exec(
		ctx,
		`UPDATE upload_sessions
		SET state = CASE state
				WHEN 'created' THEN 'uploading'
				ELSE state
			END,
			updated_at = now()
		WHERE id = $1
			AND state IN ('created', 'uploading')`,
		uploadID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	_, err = scanSession(tx.QueryRow(
		ctx,
		`SELECT `+sessionColumns+`
		FROM upload_sessions
		WHERE id = $1`,
		uploadID,
	))
	if err != nil {
		return err
	}

	return fmt.Errorf("%w: upload session does not accept parts", domain.ErrFailedPrecondition)
}

// completeSession marks the upload session completed and queues a CAS ingest
// job, treating a session that is already past completion idempotently and
// returning ErrFailedPrecondition for terminal or pre-completion states.
func completeSession(ctx context.Context, tx pgx.Tx, uploadID uuid.UUID) (domain.Session, error) {
	session, err := scanSession(tx.QueryRow(
		ctx,
		`UPDATE upload_sessions
		SET state = 'completed',
			completed_at = COALESCE(completed_at, now()),
			updated_at = now()
		WHERE id = $1
			AND state IN ('created', 'uploading')
		RETURNING `+sessionColumns,
		uploadID,
	))
	if err == nil {
		return queueIngestJob(ctx, tx, session)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, err
	}

	session, err = scanSession(tx.QueryRow(
		ctx,
		`SELECT `+sessionColumns+`
		FROM upload_sessions
		WHERE id = $1`,
		uploadID,
	))
	if err != nil {
		return domain.Session{}, err
	}

	switch session.State {
	case domain.SessionStateCompleted, domain.SessionStateIngesting, domain.SessionStateReady:
		return session, nil
	case domain.SessionStateCreated,
		domain.SessionStateUploading,
		domain.SessionStateFailed,
		domain.SessionStateAborted:
		return domain.Session{}, fmt.Errorf("%w: upload session cannot be completed from %s",
			domain.ErrFailedPrecondition,
			session.State,
		)
	}

	return domain.Session{}, fmt.Errorf("%w: upload session cannot be completed from %s",
		domain.ErrFailedPrecondition,
		session.State,
	)
}

// abortSession transitions the upload session to aborted from created or
// uploading and returns ErrFailedPrecondition when the session is in any
// other state.
func abortSession(ctx context.Context, tx pgx.Tx, uploadID uuid.UUID) (domain.Session, error) {
	session, err := scanSession(tx.QueryRow(
		ctx,
		`UPDATE upload_sessions
		SET state = 'aborted',
			aborted_at = now(),
			updated_at = now()
		WHERE id = $1
			AND state IN ('created', 'uploading')
		RETURNING `+sessionColumns,
		uploadID,
	))
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, err
	}

	session, err = scanSession(tx.QueryRow(
		ctx,
		`SELECT `+sessionColumns+`
		FROM upload_sessions
		WHERE id = $1`,
		uploadID,
	))
	if err != nil {
		return domain.Session{}, err
	}

	return domain.Session{}, fmt.Errorf("%w: upload session cannot be aborted from %s",
		domain.ErrFailedPrecondition,
		session.State,
	)
}

// queueIngestJob inserts a queued CAS ingest job for the completed upload
// session, treating an existing job for the same upload as a no-op.
func queueIngestJob(ctx context.Context, tx pgx.Tx, session domain.Session) (domain.Session, error) {
	_, err := tx.Exec(
		ctx,
		`INSERT INTO cas_ingest_jobs (id, upload_id, state)
		VALUES ($1, $2, 'queued')
		ON CONFLICT (upload_id) DO NOTHING`,
		uuid.New(),
		session.ID,
	)
	if err != nil {
		return domain.Session{}, err
	}

	return session, nil
}

// readyStorageUploadID returns the synthetic backing upload identifier stored on skipped ready sessions.
func readyStorageUploadID(id uuid.UUID) string {
	return "ready:" + id.String()
}
