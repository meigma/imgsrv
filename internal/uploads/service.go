package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/objectstore"
)

// ServiceConfig configures an upload staging service.
type ServiceConfig struct {
	// Store persists durable upload session and part state.
	Store Store

	// Objects writes staged upload bytes to object storage.
	Objects objectstore.Store

	// Now returns the current time for generated upload state. Nil selects time.Now.
	Now func() time.Time
}

// Service coordinates client-facing upload staging before CAS ingest.
type Service struct {
	store   Store
	objects objectstore.Store
	now     func() time.Time
}

// NewService constructs an upload staging service from config.
func NewService(config ServiceConfig) *Service {
	now := config.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store:   config.Store,
		objects: config.Objects,
		now:     now,
	}
}

// BeginUploadParams starts a staged multipart upload for an expected digest.
type BeginUploadParams struct {
	// ID optionally fixes the upload identity. uuid.Nil lets the service choose one.
	ID uuid.UUID

	// ExpectedDigest is the digest the staged object must verify against during CAS ingest.
	ExpectedDigest Digest

	// ExpectedSizeBytes is the declared final object size.
	ExpectedSizeBytes int64

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string

	// ExpiresAt is when unfinished upload state is eligible for cleanup.
	ExpiresAt time.Time
}

// PutUploadPartParams streams one client upload part into staging storage.
type PutUploadPartParams struct {
	// UploadID identifies the upload session.
	UploadID uuid.UUID

	// PartNumber is the S3-compatible multipart part number.
	PartNumber int

	// Body streams the part bytes.
	Body io.Reader

	// SizeBytes is the part size.
	SizeBytes int64
}

// CompleteUploadPart identifies a staged upload part to commit.
type CompleteUploadPart struct {
	// Number is the S3-compatible multipart part number.
	Number int

	// ETag is the backing object-store part ETag returned by PutUploadPart.
	ETag string

	// SizeBytes is the accepted part size.
	SizeBytes int64
}

// CompleteUploadParams completes staging storage and queues CAS ingest.
type CompleteUploadParams struct {
	// UploadID identifies the upload session.
	UploadID uuid.UUID

	// Parts are the accepted upload parts to commit.
	Parts []CompleteUploadPart
}

// AbortUploadParams aborts an upload before CAS ingest starts.
type AbortUploadParams struct {
	// UploadID identifies the upload session.
	UploadID uuid.UUID
}

// GetUploadParams looks up current upload state.
type GetUploadParams struct {
	// UploadID identifies the upload session.
	UploadID uuid.UUID
}

// BeginUpload starts durable upload state and backing multipart staging storage.
func (service *Service) BeginUpload(ctx context.Context, params BeginUploadParams) (Session, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return Session{}, depErr
	}
	if validationErr := validateBeginUploadParams(params, service.now()); validationErr != nil {
		return Session{}, validationErr
	}

	id := params.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	stagingKey := StagingKey(id)

	upload, err := objects.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadParams{
		Key: stagingKey,
	})
	if err != nil {
		return Session{}, err
	}

	session, err := store.CreateSession(ctx, CreateSessionParams{
		ID:                id,
		ExpectedDigest:    params.ExpectedDigest,
		ExpectedSizeBytes: params.ExpectedSizeBytes,
		StorageUploadID:   upload.UploadID,
		MediaTypeHint:     params.MediaTypeHint,
		FilenameHint:      params.FilenameHint,
		ExpiresAt:         params.ExpiresAt,
	})
	if err != nil {
		abortErr := objects.AbortMultipartUpload(context.WithoutCancel(ctx), objectstore.AbortMultipartUploadParams{
			Key:      stagingKey,
			UploadID: upload.UploadID,
		})
		if abortErr != nil {
			return Session{}, errors.Join(err, fmt.Errorf("abort staged multipart upload: %w", abortErr))
		}

		return Session{}, err
	}

	return session, nil
}

// PutUploadPart stores or replaces one upload part in staging storage.
func (service *Service) PutUploadPart(ctx context.Context, params PutUploadPartParams) (Part, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return Part{}, depErr
	}
	if validationErr := validatePutUploadPartParams(params); validationErr != nil {
		return Part{}, validationErr
	}

	session, err := store.GetSession(ctx, GetSessionParams{ID: params.UploadID})
	if err != nil {
		return Part{}, err
	}
	if stateErr := requireUploadAcceptsParts(session); stateErr != nil {
		return Part{}, stateErr
	}
	if expiryErr := requireUploadNotExpired(session, service.now()); expiryErr != nil {
		return Part{}, expiryErr
	}

	part, err := objects.PutPart(ctx, objectstore.PutPartParams{
		Key:        session.StagingKey,
		UploadID:   session.StorageUploadID,
		PartNumber: params.PartNumber,
		Body:       params.Body,
		SizeBytes:  params.SizeBytes,
	})
	if err != nil {
		return Part{}, err
	}

	return store.PutPart(ctx, PutPartParams{
		UploadID:   params.UploadID,
		PartNumber: part.Number,
		ETag:       part.ETag,
		SizeBytes:  part.SizeBytes,
	})
}

// CompleteUpload completes the staged multipart object and queues CAS ingest.
func (service *Service) CompleteUpload(ctx context.Context, params CompleteUploadParams) (Session, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return Session{}, depErr
	}
	if validationErr := validateCompleteUploadParams(params); validationErr != nil {
		return Session{}, validationErr
	}

	session, err := store.GetSession(ctx, GetSessionParams{ID: params.UploadID})
	if err != nil {
		return Session{}, err
	}
	if shouldReturnCompletedSession(session.State) {
		return session, nil
	}
	if stateErr := requireUploadCanComplete(session); stateErr != nil {
		return Session{}, stateErr
	}

	parts, sizeBytes, err := normalizeCompleteUploadParts(params.Parts)
	if err != nil {
		return Session{}, err
	}
	if sizeBytes != session.ExpectedSizeBytes {
		return Session{}, fmt.Errorf(
			"%w: complete part sizes total %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			sizeBytes,
			session.ExpectedSizeBytes,
		)
	}
	if uploadExpired(session, service.now()) {
		return recoverExpiredUploadCompletion(ctx, store, objects, session)
	}

	recovered, ok, err := completeMultipartOrRecover(ctx, store, objects, session, parts)
	if err != nil {
		return Session{}, err
	}
	if ok {
		return recovered, nil
	}

	return store.CompleteSession(ctx, CompleteSessionParams{ID: params.UploadID})
}

// AbortUpload aborts staging storage and marks durable upload state aborted.
func (service *Service) AbortUpload(ctx context.Context, params AbortUploadParams) (Session, error) {
	store, objects, depErr := service.dependencies()
	if depErr != nil {
		return Session{}, depErr
	}
	if validationErr := validateAbortUploadParams(params); validationErr != nil {
		return Session{}, validationErr
	}

	session, err := store.GetSession(ctx, GetSessionParams{ID: params.UploadID})
	if err != nil {
		return Session{}, err
	}
	if session.State == SessionStateAborted {
		return session, nil
	}
	if stateErr := requireUploadCanAbort(session); stateErr != nil {
		return Session{}, stateErr
	}

	recovered, ok, err := abortMultipartOrRecover(ctx, store, objects, session)
	if err != nil {
		return Session{}, err
	}
	if ok {
		return recovered, nil
	}

	return store.AbortSession(ctx, AbortSessionParams{ID: params.UploadID})
}

// GetUpload returns current durable upload state.
func (service *Service) GetUpload(ctx context.Context, params GetUploadParams) (Session, error) {
	store, _, depErr := service.dependencies()
	if depErr != nil {
		return Session{}, depErr
	}
	if validationErr := validateGetUploadParams(params); validationErr != nil {
		return Session{}, validationErr
	}

	return store.GetSession(ctx, GetSessionParams{ID: params.UploadID})
}

func (service *Service) dependencies() (Store, objectstore.Store, error) {
	if service == nil {
		return nil, nil, errors.New("uploads service is not configured")
	}
	if service.store == nil {
		return nil, nil, errors.New("uploads store is required")
	}
	if service.objects == nil {
		return nil, nil, errors.New("object store is required")
	}

	return service.store, service.objects, nil
}

func validateBeginUploadParams(params BeginUploadParams, now time.Time) error {
	if err := ValidateDigest(params.ExpectedDigest); err != nil {
		return err
	}
	if err := ValidateNonNegativeSize("expected size", params.ExpectedSizeBytes); err != nil {
		return err
	}
	if err := ValidateOptionalText("media type hint", params.MediaTypeHint); err != nil {
		return err
	}
	if err := ValidateOptionalText("filename hint", params.FilenameHint); err != nil {
		return err
	}
	if params.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires at is required", ErrInvalid)
	}
	if !params.ExpiresAt.After(now) {
		return fmt.Errorf("%w: expires at must be in the future", ErrInvalid)
	}

	return nil
}

func validatePutUploadPartParams(params PutUploadPartParams) error {
	if err := validateUploadID(params.UploadID); err != nil {
		return err
	}
	if err := ValidatePartNumber(params.PartNumber); err != nil {
		return err
	}
	if params.Body == nil {
		return fmt.Errorf("%w: body is required", ErrInvalid)
	}
	if err := objectstore.ValidateMultipartPartSize("part size", params.SizeBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func validateCompleteUploadParams(params CompleteUploadParams) error {
	return validateUploadID(params.UploadID)
}

func validateAbortUploadParams(params AbortUploadParams) error {
	return validateUploadID(params.UploadID)
}

func validateGetUploadParams(params GetUploadParams) error {
	return validateUploadID(params.UploadID)
}

func validateUploadID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: upload id is required", ErrInvalid)
	}

	return nil
}

func requireUploadAcceptsParts(session Session) error {
	if session.State == SessionStateCreated || session.State == SessionStateUploading {
		return nil
	}

	return fmt.Errorf("%w: upload session does not accept parts from %s", ErrFailedPrecondition, session.State)
}

func shouldReturnCompletedSession(state SessionState) bool {
	return state == SessionStateCompleted || state == SessionStateIngesting || state == SessionStateReady
}

func requireUploadCanComplete(session Session) error {
	if session.State == SessionStateCreated || session.State == SessionStateUploading {
		return nil
	}

	return fmt.Errorf("%w: upload session cannot be completed from %s", ErrFailedPrecondition, session.State)
}

func requireUploadCanAbort(session Session) error {
	if session.State == SessionStateCreated || session.State == SessionStateUploading {
		return nil
	}

	return fmt.Errorf("%w: upload session cannot be aborted from %s", ErrFailedPrecondition, session.State)
}

func requireUploadNotExpired(session Session, now time.Time) error {
	if uploadExpired(session, now) {
		return uploadExpiredError(session)
	}

	return nil
}

func uploadExpired(session Session, now time.Time) bool {
	return !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now)
}

func uploadExpiredError(session Session) error {
	return fmt.Errorf("%w: upload session expired at %s", ErrFailedPrecondition, session.ExpiresAt.Format(time.RFC3339))
}

func recoverExpiredUploadCompletion(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	session Session,
) (Session, error) {
	recovered, err := recoverCompletedStagedObject(ctx, store, objects, session)
	if err == nil {
		return recovered, nil
	}
	if errors.Is(err, objectstore.ErrNotFound) {
		return Session{}, uploadExpiredError(session)
	}

	return Session{}, err
}

func completeMultipartOrRecover(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	session Session,
	parts []objectstore.CompletePart,
) (Session, bool, error) {
	_, err := objects.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadParams{
		Key:      session.StagingKey,
		UploadID: session.StorageUploadID,
		Parts:    parts,
	})
	if err == nil {
		return Session{}, false, nil
	}
	if !errors.Is(err, objectstore.ErrNotFound) {
		return Session{}, false, err
	}

	recovered, recoverErr := recoverCompletedStagedObject(ctx, store, objects, session)
	if recoverErr == nil {
		return recovered, true, nil
	}
	if errors.Is(recoverErr, objectstore.ErrNotFound) {
		return Session{}, false, err
	}

	return Session{}, false, errors.Join(err, fmt.Errorf("recover completed staged object: %w", recoverErr))
}

func abortMultipartOrRecover(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	session Session,
) (Session, bool, error) {
	err := objects.AbortMultipartUpload(ctx, objectstore.AbortMultipartUploadParams{
		Key:      session.StagingKey,
		UploadID: session.StorageUploadID,
	})
	if err == nil {
		return Session{}, false, nil
	}
	if !errors.Is(err, objectstore.ErrNotFound) {
		return Session{}, false, err
	}

	recovered, recoverErr := recoverCompletedStagedObject(ctx, store, objects, session)
	if recoverErr == nil {
		return recovered, true, nil
	}
	if errors.Is(recoverErr, objectstore.ErrNotFound) {
		return Session{}, false, nil
	}

	return Session{}, false, errors.Join(err, fmt.Errorf("recover completed staged object: %w", recoverErr))
}

func recoverCompletedStagedObject(
	ctx context.Context,
	store Store,
	objects objectstore.Store,
	session Session,
) (Session, error) {
	info, err := objects.StatObject(ctx, objectstore.StatObjectParams{Key: session.StagingKey})
	if err != nil {
		return Session{}, err
	}
	if info.SizeBytes != session.ExpectedSizeBytes {
		return Session{}, fmt.Errorf(
			"%w: staged object size is %d bytes, expected %d bytes",
			ErrFailedPrecondition,
			info.SizeBytes,
			session.ExpectedSizeBytes,
		)
	}

	return store.CompleteSession(ctx, CompleteSessionParams{ID: session.ID})
}

func normalizeCompleteUploadParts(parts []CompleteUploadPart) ([]objectstore.CompletePart, int64, error) {
	completeParts := make([]objectstore.CompletePart, 0, len(parts))
	for _, part := range parts {
		completeParts = append(completeParts, objectstore.CompletePart{
			Number:    part.Number,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		})
	}

	normalized, err := objectstore.NormalizeCompleteParts(completeParts)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	var sizeBytes int64
	for _, part := range normalized {
		sizeBytes += part.SizeBytes
	}

	return normalized, sizeBytes, nil
}
