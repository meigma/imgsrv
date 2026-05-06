package cas_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	casmocks "github.com/meigma/imgsrv/internal/cas/mocks"
	"github.com/meigma/imgsrv/internal/objectstore"
	objectmocks "github.com/meigma/imgsrv/internal/objectstore/mocks"
	"github.com/meigma/imgsrv/internal/uploads"
)

const stagedBody = "verified bytes"

func jobIDFixture() uuid.UUID {
	return uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
}

func uploadIDFixture() uuid.UUID {
	return uuid.MustParse("11111111-2222-3333-4444-555555555555")
}

func digestFixture() uploads.Digest {
	return digestFor(stagedBody)
}

func digestFor(body string) uploads.Digest {
	sum := sha256.Sum256([]byte(body))
	return uploads.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

type serviceTestContext struct {
	store   *casmocks.MockStore
	objects *objectmocks.MockStore
	service *cas.Service
}

func newServiceTestContext(t *testing.T) *serviceTestContext {
	t.Helper()

	store := casmocks.NewMockStore(t)
	objects := objectmocks.NewMockStore(t)
	objects.EXPECT().
		DeleteObject(mock.Anything, mock.Anything).
		Return(objectstore.ErrNotFound).
		Maybe()

	return &serviceTestContext{
		store:   store,
		objects: objects,
		service: cas.NewService(cas.ServiceConfig{
			Store:   store,
			Objects: objects,
		}),
	}
}

func TestServiceCommitStagedUploadVerifiesCopiesAndSucceeds(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)
	wantJob := ingestJob(uploads.IngestJobStateSucceeded)

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, objectstore.CopyObjectParams{
			SourceKey:           params.StagingKey,
			IfSourceETag:        objectETag,
			DestinationKey:      storageKey,
			IfDestinationAbsent: true,
		}).
		Return(objectstore.ObjectInfo{Key: storageKey, SizeBytes: int64(len(stagedBody))}, nil)
	tc.store.EXPECT().
		SucceedIngestJob(mock.Anything, uploads.SucceedIngestJobParams{
			ID:         params.JobID,
			Digest:     params.ExpectedDigest,
			SizeBytes:  params.ExpectedSizeBytes,
			StorageKey: storageKey,
		}).
		Return(wantJob, nil)
	tc.objects.EXPECT().
		DeleteObject(
			mock.MatchedBy(func(ctx context.Context) bool {
				_, ok := ctx.Deadline()
				return ok
			}),
			objectstore.DeleteObjectParams{Key: params.StagingKey},
		).
		Return(objectstore.ErrNotFound)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, wantJob, got.Job)
	assert.Equal(t, params.ExpectedDigest, got.Digest)
	assert.Equal(t, params.ExpectedSizeBytes, got.SizeBytes)
	assert.Equal(t, storageKey, got.StorageKey)
}

func TestServiceCommitStagedUploadAcceptsExistingCASDestination(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)
	wantJob := ingestJob(uploads.IngestJobStateSucceeded)

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, errors.Join(objectstore.ErrAlreadyExists, errors.New("exists")))
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(blobFixture(), nil)
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: storageKey}).
		Return(objectstore.ObjectInfo{Key: storageKey, SizeBytes: params.ExpectedSizeBytes}, nil)
	tc.store.EXPECT().
		SucceedIngestJob(mock.Anything, uploads.SucceedIngestJobParams{
			ID:         params.JobID,
			Digest:     params.ExpectedDigest,
			SizeBytes:  params.ExpectedSizeBytes,
			StorageKey: storageKey,
		}).
		Return(wantJob, nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, wantJob, got.Job)
	assert.Equal(t, storageKey, got.StorageKey)
}

func TestServiceCommitStagedUploadRecoversExistingCASDestinationAfterCopyConflict(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)
	wantJob := ingestJob(uploads.IngestJobStateSucceeded)

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, errors.Join(objectstore.ErrConflict, errors.New("precondition failed")))
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(blobFixture(), nil)
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: storageKey}).
		Return(objectstore.ObjectInfo{Key: storageKey, SizeBytes: params.ExpectedSizeBytes}, nil)
	tc.store.EXPECT().
		SucceedIngestJob(mock.Anything, uploads.SucceedIngestJobParams{
			ID:         params.JobID,
			Digest:     params.ExpectedDigest,
			SizeBytes:  params.ExpectedSizeBytes,
			StorageKey: storageKey,
		}).
		Return(wantJob, nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, wantJob, got.Job)
	assert.Equal(t, storageKey, got.StorageKey)
}

func TestServiceCommitStagedUploadHashesUntrustedExistingCASDestination(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)
	wantJob := ingestJob(uploads.IngestJobStateSucceeded)

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, errors.Join(objectstore.ErrAlreadyExists, errors.New("exists")))
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(cas.Blob{}, errors.Join(cas.ErrNotFound, errors.New("not trusted")))
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: storageKey}).
		Return(objectReader(storageKey, stagedBody), nil)
	tc.store.EXPECT().
		SucceedIngestJob(mock.Anything, uploads.SucceedIngestJobParams{
			ID:         params.JobID,
			Digest:     params.ExpectedDigest,
			SizeBytes:  params.ExpectedSizeBytes,
			StorageKey: storageKey,
		}).
		Return(wantJob, nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, wantJob, got.Job)
	assert.Equal(t, storageKey, got.StorageKey)
}

func TestServiceCommitStagedUploadFailsUntrustedExistingCASDestinationMismatch(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, errors.Join(objectstore.ErrAlreadyExists, errors.New("exists")))
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(cas.Blob{}, errors.Join(cas.ErrNotFound, errors.New("not trusted")))
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: storageKey}).
		Return(objectReader(storageKey, strings.Repeat("x", len(stagedBody))), nil)
	tc.store.EXPECT().
		FailIngestJob(mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
			return got.ID == params.JobID && strings.Contains(got.FailureMessage, "existing CAS object digest")
		})).
		Return(ingestJob(uploads.IngestJobStateFailed), nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadRejectsInvalidInputBeforeCollaborators(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.CommitStagedUpload(context.Background(), cas.CommitStagedUploadParams{})

	require.ErrorIs(t, err, cas.ErrInvalid)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadFailsJobWhenStagingObjectIsMissing(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	missingErr := errors.Join(objectstore.ErrNotFound, errors.New("missing"))

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectstore.ObjectReader{}, missingErr)
	tc.store.EXPECT().
		FailIngestJob(mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
			return got.ID == params.JobID && strings.Contains(got.FailureMessage, "missing")
		})).
		Return(ingestJob(uploads.IngestJobStateFailed), nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	require.ErrorIs(t, err, objectstore.ErrNotFound)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadFailsJobWhenDigestDoesNotMatch(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, strings.Repeat("x", len(stagedBody))), nil)
	tc.store.EXPECT().
		FailIngestJob(mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
			return got.ID == params.JobID && strings.Contains(got.FailureMessage, "digest")
		})).
		Return(ingestJob(uploads.IngestJobStateFailed), nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadFailsJobWhenSizeDoesNotMatch(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	params.ExpectedSizeBytes++

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.store.EXPECT().
		FailIngestJob(mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
			return got.ID == params.JobID && strings.Contains(got.FailureMessage, "bytes")
		})).
		Return(ingestJob(uploads.IngestJobStateFailed), nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadReturnsCopyFailureWithoutFailingJob(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	copyErr := errors.Join(objectstore.ErrConflict, errors.New("copy race"))

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, copyErr)
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(cas.Blob{}, errors.Join(cas.ErrNotFound, errors.New("not trusted")))
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: cas.StorageKey(params.ExpectedDigest)}).
		Return(objectstore.ObjectReader{}, errors.Join(objectstore.ErrNotFound, errors.New("missing destination")))

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, objectstore.ErrConflict)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadFailsJobWhenSafeCopyCannotBeProven(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReaderWithoutETag(params.StagingKey, stagedBody), nil)
	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: params.ExpectedDigest}).
		Return(cas.Blob{}, errors.Join(cas.ErrNotFound, errors.New("not trusted")))
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: cas.StorageKey(params.ExpectedDigest)}).
		Return(objectstore.ObjectReader{}, errors.Join(objectstore.ErrNotFound, errors.New("missing destination")))
	tc.store.EXPECT().
		FailIngestJob(mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
			return got.ID == params.JobID && strings.Contains(got.FailureMessage, "etag")
		})).
		Return(ingestJob(uploads.IngestJobStateFailed), nil)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.Contains(t, err.Error(), "etag")
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadReturnsSuccessStoreFailure(t *testing.T) {
	tc := newServiceTestContext(t)
	params := validCommitParams()
	storageKey := cas.StorageKey(params.ExpectedDigest)
	storeErr := errors.New("store unavailable")

	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: params.StagingKey}).
		Return(objectReader(params.StagingKey, stagedBody), nil)
	tc.objects.EXPECT().
		CopyObject(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{Key: storageKey, SizeBytes: params.ExpectedSizeBytes}, nil)
	tc.store.EXPECT().
		SucceedIngestJob(mock.Anything, mock.Anything).
		Return(uploads.IngestJob{}, storeErr)

	got, err := tc.service.CommitStagedUpload(context.Background(), params)

	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, got)
}

func TestServiceCommitStagedUploadRejectsMissingDependencies(t *testing.T) {
	params := validCommitParams()

	service := cas.NewService(cas.ServiceConfig{})
	got, err := service.CommitStagedUpload(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cas store")
	assert.Empty(t, got)

	service = cas.NewService(cas.ServiceConfig{
		Store: casmocks.NewMockStore(t),
	})
	got, err = service.CommitStagedUpload(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "object store")
	assert.Empty(t, got)
}

func TestServiceOpenBlobFetchesTrustedBlobThenOpensObject(t *testing.T) {
	tc := newServiceTestContext(t)
	byteRange := &objectstore.ByteRange{Start: 1, End: 4}
	blob := blobFixture()
	wantReader := objectReader(blob.StorageKey, stagedBody)

	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{
			Key:   blob.StorageKey,
			Range: byteRange,
		}).
		Return(wantReader, nil)

	got, err := tc.service.OpenBlob(context.Background(), cas.OpenBlobParams{
		Digest: blob.Digest,
		Range:  byteRange,
	})
	defer func() {
		if got.Body != nil {
			_ = got.Body.Close()
		}
	}()

	require.NoError(t, err)
	assert.Equal(t, wantReader.Info, got.Info)
	assert.NotNil(t, got.Body)
}

func TestServiceOpenBlobRejectsInvalidInputBeforeStore(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.OpenBlob(context.Background(), cas.OpenBlobParams{})

	require.ErrorIs(t, err, cas.ErrInvalid)
	assert.Empty(t, got)
}

func TestServiceOpenBlobReturnsStoreNotFound(t *testing.T) {
	tc := newServiceTestContext(t)
	missingErr := errors.Join(cas.ErrNotFound, errors.New("missing blob"))

	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: digestFixture()}).
		Return(cas.Blob{}, missingErr)

	got, err := tc.service.OpenBlob(context.Background(), cas.OpenBlobParams{Digest: digestFixture()})

	require.ErrorIs(t, err, cas.ErrNotFound)
	assert.Empty(t, got)
}

func TestServiceOpenBlobReturnsObjectstoreNotFound(t *testing.T) {
	tc := newServiceTestContext(t)
	blob := blobFixture()
	missingErr := errors.Join(objectstore.ErrNotFound, errors.New("missing object"))

	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: blob.StorageKey}).
		Return(objectstore.ObjectReader{}, missingErr)

	got, err := tc.service.OpenBlob(context.Background(), cas.OpenBlobParams{Digest: blob.Digest})

	require.ErrorIs(t, err, objectstore.ErrNotFound)
	assert.Empty(t, got)
}

func TestServiceOpenBlobClosesReaderOnMetadataMismatch(t *testing.T) {
	tc := newServiceTestContext(t)
	blob := blobFixture()
	body := &closeTrackingReader{Reader: strings.NewReader(stagedBody)}

	tc.store.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.objects.EXPECT().
		OpenObject(mock.Anything, objectstore.OpenObjectParams{Key: blob.StorageKey}).
		Return(objectstore.ObjectReader{
			Info: objectstore.ObjectInfo{Key: blob.StorageKey, SizeBytes: blob.SizeBytes + 1},
			Body: body,
		}, nil)

	got, err := tc.service.OpenBlob(context.Background(), cas.OpenBlobParams{Digest: blob.Digest})

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.True(t, body.closed)
	assert.Empty(t, got)
}

func validCommitParams() cas.CommitStagedUploadParams {
	return cas.CommitStagedUploadParams{
		JobID:             jobIDFixture(),
		UploadID:          uploadIDFixture(),
		StagingKey:        uploads.StagingKey(uploadIDFixture()),
		ExpectedDigest:    digestFixture(),
		ExpectedSizeBytes: int64(len(stagedBody)),
	}
}

func ingestJob(state uploads.IngestJobState) uploads.IngestJob {
	return uploads.IngestJob{
		ID:       jobIDFixture(),
		UploadID: uploadIDFixture(),
		State:    state,
	}
}

func blobFixture() cas.Blob {
	return cas.Blob{
		Digest:     digestFixture(),
		SizeBytes:  int64(len(stagedBody)),
		StorageKey: cas.StorageKey(digestFixture()),
	}
}

const objectETag = "object-etag"

func objectReader(key string, body string) objectstore.ObjectReader {
	return objectstore.ObjectReader{
		Info: objectstore.ObjectInfo{
			Key:       key,
			SizeBytes: int64(len(body)),
			ETag:      objectETag,
		},
		Body: &closeTrackingReader{Reader: strings.NewReader(body)},
	}
}

func objectReaderWithoutETag(key string, body string) objectstore.ObjectReader {
	reader := objectReader(key, body)
	reader.Info.ETag = ""
	return reader
}

type closeTrackingReader struct {
	*strings.Reader

	closed bool
}

func (reader *closeTrackingReader) Close() error {
	reader.closed = true
	return nil
}
