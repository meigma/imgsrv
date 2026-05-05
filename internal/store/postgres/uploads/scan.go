package uploads

import (
	"database/sql"
	"time"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

// rowScanner is the minimal pgx row interface used by the scan helpers in
// this package, keeping them usable with both QueryRow results and rows
// iterated from Query.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession materializes a domain.Session from a single row, translating
// nullable columns into their domain pointer or value representation.
func scanSession(row rowScanner) (domain.Session, error) {
	var session domain.Session
	var mediaTypeHint sql.NullString
	var filenameHint sql.NullString
	var completedAt sql.NullTime
	var ingestStartedAt sql.NullTime
	var readyAt sql.NullTime
	var failedAt sql.NullTime
	var abortedAt sql.NullTime
	var failureMessage sql.NullString
	var readyBlobDigest sql.NullString

	err := row.Scan(
		&session.ID,
		&session.ExpectedDigest,
		&session.ExpectedSizeBytes,
		&session.State,
		&session.StorageUploadID,
		&session.StagingKey,
		&mediaTypeHint,
		&filenameHint,
		&completedAt,
		&ingestStartedAt,
		&readyAt,
		&failedAt,
		&abortedAt,
		&session.ExpiresAt,
		&failureMessage,
		&readyBlobDigest,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return domain.Session{}, err
	}

	session.MediaTypeHint = optionalString(mediaTypeHint)
	session.FilenameHint = optionalString(filenameHint)
	session.CompletedAt = optionalTime(completedAt)
	session.IngestStartedAt = optionalTime(ingestStartedAt)
	session.ReadyAt = optionalTime(readyAt)
	session.FailedAt = optionalTime(failedAt)
	session.AbortedAt = optionalTime(abortedAt)
	session.FailureMessage = optionalString(failureMessage)
	session.ReadyBlobDigest = optionalDigest(readyBlobDigest)

	return session, nil
}

// scanPart materializes a domain.Part from a single row.
func scanPart(row rowScanner) (domain.Part, error) {
	var part domain.Part

	err := row.Scan(
		&part.UploadID,
		&part.PartNumber,
		&part.ETag,
		&part.SizeBytes,
		&part.UploadedAt,
		&part.UpdatedAt,
	)
	if err != nil {
		return domain.Part{}, err
	}

	return part, nil
}

// scanIngestJob materializes a domain.IngestJob from a single row, translating
// nullable columns into their domain pointer representation.
func scanIngestJob(row rowScanner) (domain.IngestJob, error) {
	var job domain.IngestJob
	var lockedBy sql.NullString
	var lockedAt sql.NullTime
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	var failureMessage sql.NullString
	var blobDigest sql.NullString

	err := row.Scan(
		&job.ID,
		&job.UploadID,
		&job.State,
		&job.AttemptCount,
		&job.RunAfter,
		&lockedBy,
		&lockedAt,
		&startedAt,
		&finishedAt,
		&failureMessage,
		&blobDigest,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		return domain.IngestJob{}, err
	}

	job.LockedBy = optionalString(lockedBy)
	job.LockedAt = optionalTime(lockedAt)
	job.StartedAt = optionalTime(startedAt)
	job.FinishedAt = optionalTime(finishedAt)
	job.FailureMessage = optionalString(failureMessage)
	job.BlobDigest = optionalDigest(blobDigest)

	return job, nil
}

// optionalString returns a pointer to value.String when valid, or nil.
func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

// optionalTime returns a pointer to value.Time when valid, or nil.
func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}

// optionalDigest returns a pointer to a domain.Digest derived from value when
// valid, or nil.
func optionalDigest(value sql.NullString) *domain.Digest {
	if !value.Valid {
		return nil
	}

	digest := domain.Digest(value.String)
	return &digest
}
