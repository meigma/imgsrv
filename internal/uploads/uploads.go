// Package uploads defines durable upload and CAS ingest boundaries.
package uploads

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// maxPartNumber is the maximum S3-compatible multipart part number accepted by the service.
	maxPartNumber = 10000

	// stagingUploadPrefix is the object-storage key prefix for staged upload sessions.
	stagingUploadPrefix = "staging/uploads/"
)

// Error identifies a category of upload failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrConflict means the requested upload identity already exists.
	ErrConflict Error = "upload conflict"

	// ErrFailedPrecondition means the operation violates upload state.
	ErrFailedPrecondition Error = "upload failed precondition"

	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "upload invalid input"

	// ErrNotFound means the requested upload resource does not exist.
	ErrNotFound Error = "upload not found"
)

// Store persists durable upload state and CAS ingest jobs.
type Store interface {
	// CreateSession creates durable upload state for a backing multipart upload.
	CreateSession(context.Context, CreateSessionParams) (Session, error)

	// CreateReadySession creates a terminal ready upload session without staging or ingest.
	CreateReadySession(context.Context, CreateReadySessionParams) (Session, error)

	// GetSession returns durable upload state by upload ID.
	GetSession(context.Context, GetSessionParams) (Session, error)

	// PutPart records or replaces one accepted upload part.
	PutPart(context.Context, PutPartParams) (Part, error)

	// CompleteSession marks an upload completed and queues CAS ingest.
	CompleteSession(context.Context, CompleteSessionParams) (Session, error)

	// AbortSession marks an upload aborted before CAS ingest starts.
	AbortSession(context.Context, AbortSessionParams) (Session, error)

	// ClaimIngestJob claims the next CAS ingest job for a worker.
	ClaimIngestJob(context.Context, ClaimIngestJobParams) (IngestJob, error)

	// SucceedIngestJob records a successful CAS ingest outcome.
	SucceedIngestJob(context.Context, SucceedIngestJobParams) (IngestJob, error)

	// FailIngestJob records a failed CAS ingest outcome.
	FailIngestJob(context.Context, FailIngestJobParams) (IngestJob, error)
}

// TrustedBlobLookup resolves verified CAS blobs by digest for upload short-circuit decisions.
type TrustedBlobLookup interface {
	// GetTrustedBlob returns trusted CAS blob metadata by digest.
	GetTrustedBlob(context.Context, GetTrustedBlobParams) (TrustedBlob, error)
}

// StagingKey returns the object-storage key for a staged upload session.
func StagingKey(id uuid.UUID) string {
	return stagingUploadPrefix + id.String()
}

// Digest identifies an expected or verified content blob.
type Digest string

// ParseDigest validates raw as a sha256 digest.
func ParseDigest(raw string) (Digest, error) {
	if !matches(`^sha256:[0-9a-f]{64}$`, raw) {
		return "", fmt.Errorf("%w: digest must match sha256:<64 lowercase hex chars>", ErrInvalid)
	}

	return Digest(raw), nil
}

// String returns the digest string.
func (digest Digest) String() string {
	return string(digest)
}

// SessionState is the durable upload-session lifecycle state.
type SessionState string

const (
	// SessionStateCreated means the upload session exists but has no accepted parts yet.
	SessionStateCreated SessionState = "created"

	// SessionStateUploading means at least one part has been accepted.
	SessionStateUploading SessionState = "uploading"

	// SessionStateCompleted means object storage has completed the multipart object.
	SessionStateCompleted SessionState = "completed"

	// SessionStateIngesting means a worker is verifying and promoting the staged object.
	SessionStateIngesting SessionState = "ingesting"

	// SessionStateReady means the expected digest exists as a verified CAS blob.
	SessionStateReady SessionState = "ready"

	// SessionStateFailed means ingest failed.
	SessionStateFailed SessionState = "failed"

	// SessionStateAborted means the upload was explicitly aborted before ingest.
	SessionStateAborted SessionState = "aborted"
)

// IngestJobState is the durable CAS ingest job lifecycle state.
type IngestJobState string

const (
	// IngestJobStateQueued means the job is ready to be claimed after RunAfter.
	IngestJobStateQueued IngestJobState = "queued"

	// IngestJobStateRunning means the job has been claimed by a worker.
	IngestJobStateRunning IngestJobState = "running"

	// IngestJobStateSucceeded means the job recorded a verified CAS blob.
	IngestJobStateSucceeded IngestJobState = "succeeded"

	// IngestJobStateFailed means the job finished unsuccessfully.
	IngestJobStateFailed IngestJobState = "failed"
)

// Session is a durable upload session.
type Session struct {
	// ID is the stable upload session identity.
	ID uuid.UUID

	// ExpectedDigest is the digest the uploaded bytes must verify against.
	ExpectedDigest Digest

	// ExpectedSizeBytes is the declared size of the uploaded object.
	ExpectedSizeBytes int64

	// State is the durable upload lifecycle state.
	State SessionState

	// StorageUploadID is the backing object-storage multipart upload identity.
	StorageUploadID string

	// StagingKey is the object-storage key for the completed staged upload.
	StagingKey string

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string

	// CompletedAt is set when object storage has completed the multipart object.
	CompletedAt *time.Time

	// IngestStartedAt is set when a worker starts CAS ingest.
	IngestStartedAt *time.Time

	// ReadyAt is set when the expected digest is available in CAS.
	ReadyAt *time.Time

	// FailedAt is set when ingest fails.
	FailedAt *time.Time

	// AbortedAt is set when the upload is aborted.
	AbortedAt *time.Time

	// ExpiresAt is when unfinished upload state is eligible for cleanup.
	ExpiresAt time.Time

	// FailureMessage describes the terminal failure when State is failed.
	FailureMessage *string

	// ReadyBlobDigest identifies the verified CAS blob when State is ready.
	ReadyBlobDigest *Digest

	// CreatedAt is when the upload session was created.
	CreatedAt time.Time

	// UpdatedAt is when durable upload session state last changed.
	UpdatedAt time.Time
}

// TrustedBlob is trusted CAS blob metadata used by the upload service.
type TrustedBlob struct {
	// Digest identifies the verified CAS blob.
	Digest Digest

	// SizeBytes is the verified CAS blob size.
	SizeBytes int64

	// MediaType is optional verified media-type context.
	MediaType *string
}

// Part records one accepted multipart upload part.
type Part struct {
	// UploadID identifies the parent upload session.
	UploadID uuid.UUID

	// PartNumber is the S3-compatible multipart part number.
	PartNumber int

	// ETag is the backing object-storage part ETag.
	ETag string

	// SizeBytes is the accepted part size.
	SizeBytes int64

	// UploadedAt is when the part was first accepted.
	UploadedAt time.Time

	// UpdatedAt is when the part was last replaced.
	UpdatedAt time.Time
}

// IngestJob is a durable CAS ingest job.
type IngestJob struct {
	// ID is the stable ingest job identity.
	ID uuid.UUID

	// UploadID identifies the upload session to ingest.
	UploadID uuid.UUID

	// State is the durable job lifecycle state.
	State IngestJobState

	// AttemptCount records how many times the job has been claimed.
	AttemptCount int

	// RunAfter is the earliest time the job may be claimed.
	RunAfter time.Time

	// LockedBy identifies the worker currently running the job.
	LockedBy *string

	// LockedAt is when the current worker claimed the job.
	LockedAt *time.Time

	// StartedAt is when ingest first started.
	StartedAt *time.Time

	// FinishedAt is when the job reached a terminal state.
	FinishedAt *time.Time

	// FailureMessage describes the terminal failure when State is failed.
	FailureMessage *string

	// BlobDigest identifies the verified CAS blob when State is succeeded.
	BlobDigest *Digest

	// CreatedAt is when the ingest job was created.
	CreatedAt time.Time

	// UpdatedAt is when durable ingest job state last changed.
	UpdatedAt time.Time
}

// CreateSessionParams creates durable upload state after object-storage upload initiation.
type CreateSessionParams struct {
	// ID is the caller-owned upload session identity used to derive the staging key.
	ID uuid.UUID

	// ExpectedDigest is the digest the uploaded bytes must verify against.
	ExpectedDigest Digest

	// ExpectedSizeBytes is the declared size of the uploaded object.
	ExpectedSizeBytes int64

	// StorageUploadID is the backing object-storage multipart upload identity.
	StorageUploadID string

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string

	// ExpiresAt is when unfinished upload state is eligible for cleanup.
	ExpiresAt time.Time
}

// CreateReadySessionParams creates a terminal ready upload session for a trusted blob.
type CreateReadySessionParams struct {
	// ID is the caller-owned upload session identity.
	ID uuid.UUID

	// ExpectedDigest is the digest already trusted in CAS.
	ExpectedDigest Digest

	// ExpectedSizeBytes is the declared size of the trusted CAS blob.
	ExpectedSizeBytes int64

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string

	// ExpiresAt is when unfinished upload state would be eligible for cleanup.
	ExpiresAt time.Time
}

// GetSessionParams looks up durable upload state.
type GetSessionParams struct {
	// ID identifies the upload session.
	ID uuid.UUID
}

// GetTrustedBlobParams looks up a trusted CAS blob by digest.
type GetTrustedBlobParams struct {
	// Digest identifies the trusted CAS blob.
	Digest Digest
}

// PutPartParams records or replaces an upload part.
type PutPartParams struct {
	// UploadID identifies the parent upload session.
	UploadID uuid.UUID

	// PartNumber is the S3-compatible multipart part number.
	PartNumber int

	// ETag is the backing object-storage part ETag.
	ETag string

	// SizeBytes is the accepted part size.
	SizeBytes int64
}

// CompleteSessionParams marks object-storage multipart completion.
type CompleteSessionParams struct {
	// ID identifies the upload session.
	ID uuid.UUID
}

// AbortSessionParams aborts an upload before ingest starts.
type AbortSessionParams struct {
	// ID identifies the upload session.
	ID uuid.UUID
}

// ClaimIngestJobParams claims the next queued CAS ingest job.
type ClaimIngestJobParams struct {
	// WorkerID identifies the worker claiming the job.
	WorkerID string
}

// SucceedIngestJobParams records successful CAS ingest.
type SucceedIngestJobParams struct {
	// ID identifies the ingest job.
	ID uuid.UUID

	// Digest identifies the verified CAS blob.
	Digest Digest

	// SizeBytes is the verified blob size.
	SizeBytes int64

	// StorageKey is the digest-addressed CAS storage key.
	StorageKey string

	// MediaType is optional verified blob media-type context.
	MediaType *string
}

// FailIngestJobParams records failed CAS ingest.
type FailIngestJobParams struct {
	// ID identifies the ingest job.
	ID uuid.UUID

	// FailureMessage describes why ingest failed.
	FailureMessage string
}

// BeginUploadResult describes the outcome of a begin-upload request.
type BeginUploadResult struct {
	// Session is the durable upload session to return to the caller.
	Session Session

	// Created reports whether the service created fresh multipart upload state.
	Created bool
}

// ValidateDigest validates a digest value.
func ValidateDigest(digest Digest) error {
	_, err := ParseDigest(digest.String())
	return err
}

// ValidatePartNumber validates an S3-compatible multipart part number.
func ValidatePartNumber(partNumber int) error {
	if partNumber < 1 || partNumber > maxPartNumber {
		return fmt.Errorf("%w: part number must be between 1 and %d", ErrInvalid, maxPartNumber)
	}

	return nil
}

// ValidateNonNegativeSize validates that size is not negative.
func ValidateNonNegativeSize(field string, size int64) error {
	if size < 0 {
		return fmt.Errorf("%w: %s must be non-negative", ErrInvalid, field)
	}

	return nil
}

// ValidateRequiredText validates non-empty text after trimming ASCII whitespace.
func ValidateRequiredText(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}

	return nil
}

// ValidateOptionalText validates optional non-empty text after trimming ASCII whitespace.
func ValidateOptionalText(field string, value *string) error {
	if value == nil {
		return nil
	}

	return ValidateRequiredText(field, *value)
}

// matches reports whether value matches the regular expression pattern.
func matches(pattern string, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}
