package uploads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meigma/imgsrv/internal/cas"
	domain "github.com/meigma/imgsrv/internal/uploads"
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

// GetTrustedBlob returns trusted CAS blob metadata by digest for upload short-circuit checks.
func (store *Store) GetTrustedBlob(
	ctx context.Context,
	params domain.GetTrustedBlobParams,
) (domain.TrustedBlob, error) {
	if err := validateGetTrustedBlobParams(params); err != nil {
		return domain.TrustedBlob{}, err
	}

	db, err := store.uploadDB()
	if err != nil {
		return domain.TrustedBlob{}, err
	}

	blob, err := scanTrustedBlob(db.QueryRow(
		ctx,
		`SELECT `+blobColumns+`
		FROM cas_blobs
		WHERE digest = $1`,
		params.Digest,
	))
	if err != nil {
		return domain.TrustedBlob{}, mapUploadError(err)
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

// scanTrustedBlob materializes upload short-circuit metadata from a trusted CAS blob row.
func scanTrustedBlob(row rowScanner) (domain.TrustedBlob, error) {
	var blob domain.TrustedBlob
	var mediaType sql.NullString
	var storageKey string
	var verifiedAt, createdAt time.Time

	err := row.Scan(
		&blob.Digest,
		&blob.SizeBytes,
		&storageKey,
		&mediaType,
		&verifiedAt,
		&createdAt,
	)
	if err != nil {
		return domain.TrustedBlob{}, err
	}

	_ = storageKey
	_ = verifiedAt
	_ = createdAt
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
