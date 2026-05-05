// Package cas defines content-addressed storage service boundaries.
package cas

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

// Store records CAS ingest outcomes.
type Store interface {
	SucceedIngestJob(context.Context, uploads.SucceedIngestJobParams) (uploads.IngestJob, error)
	FailIngestJob(context.Context, uploads.FailIngestJobParams) (uploads.IngestJob, error)
}

// ServiceConfig configures a CAS service.
type ServiceConfig struct {
	// Store records verified blob state and ingest job outcomes.
	Store Store

	// Objects reads staged bytes and writes digest-addressed CAS objects.
	Objects objectstore.Store
}

// Service commits staged objects into content-addressed storage.
type Service struct {
	store   Store
	objects objectstore.Store
}

// NewService constructs a CAS service from config.
func NewService(config ServiceConfig) *Service {
	return &Service{
		store:   config.Store,
		objects: config.Objects,
	}
}

// CommitStagedUploadParams commits one completed upload session into CAS.
type CommitStagedUploadParams struct {
	// JobID identifies the running CAS ingest job to complete.
	JobID uuid.UUID

	// UploadID identifies the staged upload session being committed.
	UploadID uuid.UUID

	// StagingKey is the object-store key for the completed staged upload.
	StagingKey string

	// ExpectedDigest is the digest the staged bytes must match.
	ExpectedDigest uploads.Digest

	// ExpectedSizeBytes is the size the staged bytes must match.
	ExpectedSizeBytes int64

	// MediaType is optional verified blob media-type context.
	MediaType *string
}

// CommitStagedUploadResult describes a successful CAS commit.
type CommitStagedUploadResult struct {
	// Job is the updated durable ingest job.
	Job uploads.IngestJob

	// Digest identifies the verified CAS blob.
	Digest uploads.Digest

	// SizeBytes is the verified CAS blob size.
	SizeBytes int64

	// StorageKey is the digest-addressed CAS object key.
	StorageKey string
}

// OpenBlobParams opens a verified CAS blob for reading.
type OpenBlobParams struct {
	// Digest identifies the verified CAS blob.
	Digest uploads.Digest

	// Range optionally limits the returned bytes. Nil opens the whole blob.
	Range *objectstore.ByteRange
}

// CommitStagedUpload verifies staged bytes and commits them into CAS.
func (service *Service) CommitStagedUpload(
	_ context.Context,
	_ CommitStagedUploadParams,
) (CommitStagedUploadResult, error) {
	return CommitStagedUploadResult{}, errors.ErrUnsupported
}

// OpenBlob opens a verified CAS blob for proxied download.
func (service *Service) OpenBlob(_ context.Context, _ OpenBlobParams) (objectstore.ObjectReader, error) {
	return objectstore.ObjectReader{}, errors.ErrUnsupported
}
