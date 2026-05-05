package uploads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/meigma/imgsrv/internal/cas"
)

// blobColumns enumerates the cas_blobs columns selected when scanning a
// trusted CAS blob row.
const blobColumns = `digest,
	size_bytes,
	storage_key,
	media_type,
	verified_at,
	created_at`

// GetBlob returns a trusted CAS blob by digest.
func (store *Store) GetBlob(ctx context.Context, params cas.GetBlobParams) (cas.Blob, error) {
	if err := params.Validate(); err != nil {
		return cas.Blob{}, err
	}

	db, err := store.uploadDB()
	if err != nil {
		return cas.Blob{}, err
	}

	blob, err := scanBlob(db.QueryRow(
		ctx,
		`SELECT `+blobColumns+`
		FROM cas_blobs
		WHERE digest = $1`,
		params.Digest,
	))
	if err != nil {
		return cas.Blob{}, mapCASError(err)
	}

	return blob, nil
}

// scanBlob materializes a cas.Blob from a single row, translating nullable
// columns into their domain representation.
func scanBlob(row rowScanner) (cas.Blob, error) {
	var blob cas.Blob
	var mediaType sql.NullString

	err := row.Scan(
		&blob.Digest,
		&blob.SizeBytes,
		&blob.StorageKey,
		&mediaType,
		&blob.VerifiedAt,
		&blob.CreatedAt,
	)
	if err != nil {
		return cas.Blob{}, err
	}

	blob.MediaType = optionalString(mediaType)
	return blob, nil
}

// mapCASError translates a Postgres driver error into a cas package sentinel
// error where one is defined, leaving other errors unwrapped.
func mapCASError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", cas.ErrNotFound, err)
	}

	return err
}
