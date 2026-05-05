// Package cas defines content-addressed storage service boundaries.
package cas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

const storageKeyPrefix = "cas/sha256/"

// Error identifies a category of CAS failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrFailedPrecondition means the operation violates CAS state.
	ErrFailedPrecondition Error = "cas failed precondition"

	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "cas invalid input"

	// ErrNotFound means the requested CAS resource does not exist.
	ErrNotFound Error = "cas not found"
)

// Store records CAS blob state and ingest outcomes.
type Store interface {
	// GetBlob returns a trusted CAS blob by digest.
	GetBlob(context.Context, GetBlobParams) (Blob, error)

	// SucceedIngestJob records a verified CAS commit for an ingest job.
	SucceedIngestJob(context.Context, uploads.SucceedIngestJobParams) (uploads.IngestJob, error)

	// FailIngestJob records a failed CAS commit for an ingest job.
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

type verifiedObject struct {
	info      objectstore.ObjectInfo
	digest    uploads.Digest
	sizeBytes int64
}

// Blob describes a trusted CAS blob.
type Blob struct {
	// Digest identifies the verified blob.
	Digest uploads.Digest

	// SizeBytes is the verified blob size.
	SizeBytes int64

	// StorageKey is the digest-addressed object-store key.
	StorageKey string

	// MediaType is optional verified blob media-type context.
	MediaType *string

	// VerifiedAt is when CAS ingest verified the blob bytes.
	VerifiedAt time.Time

	// CreatedAt is when the trusted blob record was created.
	CreatedAt time.Time
}

// GetBlobParams looks up a trusted CAS blob.
type GetBlobParams struct {
	// Digest identifies the verified CAS blob.
	Digest uploads.Digest
}

// Validate checks that params can look up a trusted CAS blob.
func (params GetBlobParams) Validate() error {
	return validateDigest(params.Digest)
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

// StorageKey returns the digest-addressed object-store key for digest.
func StorageKey(digest uploads.Digest) string {
	if err := uploads.ValidateDigest(digest); err != nil {
		return ""
	}

	raw, ok := strings.CutPrefix(digest.String(), "sha256:")
	if !ok {
		return ""
	}

	return storageKeyPrefix + raw[:2] + "/" + raw[2:4] + "/" + raw
}

// CommitStagedUpload verifies staged bytes and commits them into CAS.
func (service *Service) CommitStagedUpload(
	ctx context.Context,
	params CommitStagedUploadParams,
) (CommitStagedUploadResult, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return CommitStagedUploadResult{}, depErr
	}
	if validationErr := validateCommitStagedUploadParams(params); validationErr != nil {
		return CommitStagedUploadResult{}, validationErr
	}

	reader, err := objects.OpenObject(ctx, objectstore.OpenObjectParams{Key: params.StagingKey})
	if err != nil {
		if errors.Is(err, objectstore.ErrNotFound) {
			return failCommit(ctx, store, params.JobID, "staged object is missing", err)
		}

		return CommitStagedUploadResult{}, err
	}

	verified, err := verifyObjectReader(reader)
	if err != nil {
		return CommitStagedUploadResult{}, err
	}
	if verified.sizeBytes != params.ExpectedSizeBytes {
		return failCommit(
			ctx,
			store,
			params.JobID,
			fmt.Sprintf("staged object is %d bytes, expected %d bytes", verified.sizeBytes, params.ExpectedSizeBytes),
			nil,
		)
	}
	if verified.digest != params.ExpectedDigest {
		return failCommit(
			ctx,
			store,
			params.JobID,
			fmt.Sprintf("staged object digest is %s, expected %s", verified.digest, params.ExpectedDigest),
			nil,
		)
	}

	storageKey := StorageKey(params.ExpectedDigest)
	committed, err := ensureCASObject(ctx, store, objects, ensureCASObjectParams{
		Digest:             params.ExpectedDigest,
		SizeBytes:          params.ExpectedSizeBytes,
		StagingKey:         params.StagingKey,
		StorageKey:         storageKey,
		VerifiedSourceETag: verified.info.ETag,
	})
	if err != nil {
		if errors.Is(err, ErrFailedPrecondition) {
			return failCommit(ctx, store, params.JobID, failureMessage(err), nil)
		}

		return CommitStagedUploadResult{}, err
	}
	if matchErr := requireCASObjectInfo(committed, storageKey, params.ExpectedSizeBytes); matchErr != nil {
		return failCommit(ctx, store, params.JobID, failureMessage(matchErr), nil)
	}

	job, err := store.SucceedIngestJob(ctx, uploads.SucceedIngestJobParams{
		ID:         params.JobID,
		Digest:     params.ExpectedDigest,
		SizeBytes:  params.ExpectedSizeBytes,
		StorageKey: storageKey,
		MediaType:  params.MediaType,
	})
	if err != nil {
		return CommitStagedUploadResult{}, err
	}

	return CommitStagedUploadResult{
		Job:        job,
		Digest:     params.ExpectedDigest,
		SizeBytes:  params.ExpectedSizeBytes,
		StorageKey: storageKey,
	}, nil
}

// OpenBlob opens a verified CAS blob for proxied download.
func (service *Service) OpenBlob(ctx context.Context, params OpenBlobParams) (objectstore.ObjectReader, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return objectstore.ObjectReader{}, depErr
	}
	if validationErr := validateOpenBlobParams(params); validationErr != nil {
		return objectstore.ObjectReader{}, validationErr
	}

	blob, err := store.GetBlob(ctx, GetBlobParams{Digest: params.Digest})
	if err != nil {
		return objectstore.ObjectReader{}, err
	}

	reader, err := objects.OpenObject(ctx, objectstore.OpenObjectParams{
		Key:   blob.StorageKey,
		Range: params.Range,
	})
	if err != nil {
		return objectstore.ObjectReader{}, err
	}
	if matchErr := requireOpenedBlobInfo(reader.Info, blob); matchErr != nil {
		return objectstore.ObjectReader{}, closeReaderAfterError(reader.Body, matchErr)
	}

	return reader, nil
}

func (service *Service) dependencies() (Store, objectstore.Store, error) {
	if service == nil {
		return nil, nil, errors.New("cas service is not configured")
	}
	if service.store == nil {
		return nil, nil, errors.New("cas store is required")
	}
	if service.objects == nil {
		return nil, nil, errors.New("object store is required")
	}

	return service.store, service.objects, nil
}

func validateCommitStagedUploadParams(params CommitStagedUploadParams) error {
	if params.JobID == uuid.Nil {
		return fmt.Errorf("%w: ingest job id is required", ErrInvalid)
	}
	if params.UploadID == uuid.Nil {
		return fmt.Errorf("%w: upload id is required", ErrInvalid)
	}
	if err := validateObjectKey("staging key", params.StagingKey); err != nil {
		return err
	}
	if want := uploads.StagingKey(params.UploadID); params.StagingKey != want {
		return fmt.Errorf("%w: staging key must be %s", ErrInvalid, want)
	}
	if err := validateDigest(params.ExpectedDigest); err != nil {
		return err
	}
	if err := uploads.ValidateNonNegativeSize("expected size", params.ExpectedSizeBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := uploads.ValidateOptionalText("media type", params.MediaType); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func validateOpenBlobParams(params OpenBlobParams) error {
	if err := validateDigest(params.Digest); err != nil {
		return err
	}
	if params.Range == nil {
		return nil
	}
	if err := params.Range.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func validateDigest(digest uploads.Digest) error {
	if err := uploads.ValidateDigest(digest); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func validateObjectKey(field string, key string) error {
	if err := objectstore.ValidateRequiredText(field, key); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func verifyObjectReader(reader objectstore.ObjectReader) (verifiedObject, error) {
	if reader.Body == nil {
		return verifiedObject{}, errors.New("object reader body is required")
	}

	hasher := sha256.New()
	sizeBytes, copyErr := io.Copy(hasher, reader.Body)
	closeErr := reader.Body.Close()
	if copyErr != nil {
		return verifiedObject{}, copyErr
	}
	if closeErr != nil {
		return verifiedObject{}, closeErr
	}

	return verifiedObject{
		info:      reader.Info,
		digest:    uploads.Digest("sha256:" + hex.EncodeToString(hasher.Sum(nil))),
		sizeBytes: sizeBytes,
	}, nil
}

type ensureCASObjectParams struct {
	Digest             uploads.Digest
	SizeBytes          int64
	StagingKey         string
	StorageKey         string
	VerifiedSourceETag string
}

func ensureCASObject(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	params ensureCASObjectParams,
) (objectstore.ObjectInfo, error) {
	if strings.TrimSpace(params.VerifiedSourceETag) == "" {
		recovered, err := recoverExistingCASObject(ctx, store, objects, params)
		if err == nil {
			return recovered, nil
		}
		if errors.Is(err, objectstore.ErrNotFound) {
			return objectstore.ObjectInfo{}, fmt.Errorf(
				"%w: verified staged object etag is required",
				ErrFailedPrecondition,
			)
		}

		return objectstore.ObjectInfo{}, err
	}

	info, err := objects.CopyObject(ctx, objectstore.CopyObjectParams{
		SourceKey:           params.StagingKey,
		IfSourceETag:        params.VerifiedSourceETag,
		DestinationKey:      params.StorageKey,
		IfDestinationAbsent: true,
	})
	if err == nil {
		return info, nil
	}
	if !errors.Is(err, objectstore.ErrAlreadyExists) && !errors.Is(err, objectstore.ErrConflict) {
		return objectstore.ObjectInfo{}, err
	}

	recovered, recoverErr := recoverExistingCASObject(ctx, store, objects, params)
	if recoverErr == nil {
		return recovered, nil
	}
	if errors.Is(err, objectstore.ErrConflict) && errors.Is(recoverErr, objectstore.ErrNotFound) {
		return objectstore.ObjectInfo{}, err
	}
	if errors.Is(recoverErr, ErrFailedPrecondition) {
		return objectstore.ObjectInfo{}, recoverErr
	}

	return objectstore.ObjectInfo{}, errors.Join(err, fmt.Errorf("recover existing CAS object: %w", recoverErr))
}

func recoverExistingCASObject(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	params ensureCASObjectParams,
) (objectstore.ObjectInfo, error) {
	blob, err := store.GetBlob(ctx, GetBlobParams{Digest: params.Digest})
	if err == nil {
		if matchErr := requireTrustedBlob(blob, params.StorageKey, params.SizeBytes); matchErr != nil {
			return objectstore.ObjectInfo{}, matchErr
		}

		return objects.StatObject(ctx, objectstore.StatObjectParams{Key: blob.StorageKey})
	}
	if !errors.Is(err, ErrNotFound) {
		return objectstore.ObjectInfo{}, err
	}

	verified, err := verifyExistingCASObject(ctx, objects, params.StorageKey)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if verified.sizeBytes != params.SizeBytes {
		return objectstore.ObjectInfo{}, fmt.Errorf(
			"%w: existing CAS object is %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			verified.sizeBytes,
			params.SizeBytes,
		)
	}
	if verified.digest != params.Digest {
		return objectstore.ObjectInfo{}, fmt.Errorf(
			"%w: existing CAS object digest is %s, expected %s",
			ErrFailedPrecondition,
			verified.digest,
			params.Digest,
		)
	}

	return verified.info, nil
}

func verifyExistingCASObject(
	ctx context.Context,
	objects objectstore.Store,
	storageKey string,
) (verifiedObject, error) {
	reader, err := objects.OpenObject(ctx, objectstore.OpenObjectParams{Key: storageKey})
	if err != nil {
		return verifiedObject{}, err
	}

	return verifyObjectReader(reader)
}

func requireTrustedBlob(blob Blob, storageKey string, sizeBytes int64) error {
	if blob.StorageKey != storageKey {
		return fmt.Errorf(
			"%w: trusted CAS blob key is %s, expected %s",
			ErrFailedPrecondition,
			blob.StorageKey,
			storageKey,
		)
	}
	if blob.SizeBytes != sizeBytes {
		return fmt.Errorf(
			"%w: trusted CAS blob is %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			blob.SizeBytes,
			sizeBytes,
		)
	}

	return nil
}

func requireCASObjectInfo(info objectstore.ObjectInfo, storageKey string, sizeBytes int64) error {
	if info.Key != storageKey {
		return fmt.Errorf("%w: CAS object key is %s, expected %s", ErrFailedPrecondition, info.Key, storageKey)
	}
	if info.SizeBytes != sizeBytes {
		return fmt.Errorf(
			"%w: CAS object is %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			info.SizeBytes,
			sizeBytes,
		)
	}

	return nil
}

func requireOpenedBlobInfo(info objectstore.ObjectInfo, blob Blob) error {
	if info.Key != blob.StorageKey {
		return fmt.Errorf("%w: opened object key is %s, expected %s", ErrFailedPrecondition, info.Key, blob.StorageKey)
	}
	if info.SizeBytes != blob.SizeBytes {
		return fmt.Errorf(
			"%w: opened object is %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			info.SizeBytes,
			blob.SizeBytes,
		)
	}

	return nil
}

func failCommit(
	ctx context.Context,
	store Store,
	jobID uuid.UUID,
	message string,
	cause error,
) (CommitStagedUploadResult, error) {
	failureErr := fmt.Errorf("%w: %s", ErrFailedPrecondition, message)
	if cause != nil {
		failureErr = errors.Join(failureErr, cause)
	}

	_, err := store.FailIngestJob(ctx, uploads.FailIngestJobParams{
		ID:             jobID,
		FailureMessage: message,
	})
	if err != nil {
		return CommitStagedUploadResult{}, errors.Join(failureErr, fmt.Errorf("record failed CAS ingest: %w", err))
	}

	return CommitStagedUploadResult{}, failureErr
}

func failureMessage(err error) string {
	return strings.TrimPrefix(err.Error(), ErrFailedPrecondition.Error()+": ")
}

func closeReaderAfterError(reader io.ReadCloser, err error) error {
	if reader == nil {
		return err
	}
	if closeErr := reader.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}

	return err
}
