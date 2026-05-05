package promote_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	casmocks "github.com/meigma/imgsrv/internal/cas/mocks"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/objectstore"
	objectmocks "github.com/meigma/imgsrv/internal/objectstore/mocks"
	"github.com/meigma/imgsrv/internal/uploads"
	uploadmocks "github.com/meigma/imgsrv/internal/uploads/mocks"
)

func TestJobRunOncePromotesClaimedUpload(t *testing.T) {
	tc := newTestContext(t)
	payload := []byte("cas promotion payload")
	digest := digestFor(t, payload)
	uploadID := uuid.New()
	jobID := uuid.New()
	mediaType := "application/octet-stream"
	session := uploadSession(uploadID, digest, payload, &mediaType)
	ingestJob := uploads.IngestJob{
		ID:       jobID,
		UploadID: uploadID,
		State:    uploads.IngestJobStateRunning,
	}
	storageKey := cas.StorageKey(digest)

	tc.uploads.On("ClaimIngestJob", mock.Anything, uploads.ClaimIngestJobParams{
		WorkerID: "worker-a",
	}).Return(ingestJob, nil)
	tc.uploads.On("GetSession", mock.Anything, uploads.GetSessionParams{
		ID: uploadID,
	}).Return(session, nil)
	tc.objects.On("OpenObject", mock.Anything, objectstore.OpenObjectParams{
		Key: session.StagingKey,
	}).Return(objectstore.ObjectReader{
		Info: objectstore.ObjectInfo{
			Key:       session.StagingKey,
			SizeBytes: int64(len(payload)),
			ETag:      "staging-etag",
		},
		Body: nopReadCloser(payload),
	}, nil)
	tc.objects.On("CopyObject", mock.Anything, objectstore.CopyObjectParams{
		SourceKey:           session.StagingKey,
		IfSourceETag:        "staging-etag",
		DestinationKey:      storageKey,
		IfDestinationAbsent: true,
	}).Return(objectstore.ObjectInfo{
		Key:       storageKey,
		SizeBytes: int64(len(payload)),
		ETag:      "cas-etag",
	}, nil)
	tc.casStore.On("SucceedIngestJob", mock.Anything, uploads.SucceedIngestJobParams{
		ID:         jobID,
		Digest:     digest,
		SizeBytes:  int64(len(payload)),
		StorageKey: storageKey,
		MediaType:  &mediaType,
	}).Return(uploads.IngestJob{
		ID:       jobID,
		UploadID: uploadID,
		State:    uploads.IngestJobStateSucceeded,
	}, nil)

	got, err := tc.job.RunOnce(context.Background(), "worker-a")

	require.NoError(t, err)
	assert.True(t, got.Worked)
}

func TestJobRunOnceTreatsNoQueuedJobAsIdle(t *testing.T) {
	tc := newTestContext(t)
	tc.uploads.On("ClaimIngestJob", mock.Anything, uploads.ClaimIngestJobParams{
		WorkerID: "worker-a",
	}).Return(uploads.IngestJob{}, fmt.Errorf("%w: no queued ingest job", uploads.ErrNotFound))

	got, err := tc.job.RunOnce(context.Background(), "worker-a")

	require.NoError(t, err)
	assert.False(t, got.Worked)
}

func TestJobRunOnceRecordsFailureWhenStagedObjectIsMissing(t *testing.T) {
	tc := newTestContext(t)
	payload := []byte("cas promotion payload")
	digest := digestFor(t, payload)
	uploadID := uuid.New()
	jobID := uuid.New()
	session := uploadSession(uploadID, digest, payload, nil)
	ingestJob := uploads.IngestJob{
		ID:       jobID,
		UploadID: uploadID,
		State:    uploads.IngestJobStateRunning,
	}

	tc.uploads.On("ClaimIngestJob", mock.Anything, uploads.ClaimIngestJobParams{
		WorkerID: "worker-a",
	}).Return(ingestJob, nil)
	tc.uploads.On("GetSession", mock.Anything, uploads.GetSessionParams{
		ID: uploadID,
	}).Return(session, nil)
	tc.objects.On("OpenObject", mock.Anything, objectstore.OpenObjectParams{
		Key: session.StagingKey,
	}).Return(objectstore.ObjectReader{}, objectstore.ErrNotFound)
	tc.casStore.On("FailIngestJob", mock.Anything, mock.MatchedBy(func(got uploads.FailIngestJobParams) bool {
		return got.ID == jobID && strings.Contains(got.FailureMessage, "staged object is missing")
	})).Return(uploads.IngestJob{
		ID:       jobID,
		UploadID: uploadID,
		State:    uploads.IngestJobStateFailed,
	}, nil)

	got, err := tc.job.RunOnce(context.Background(), "worker-a")

	require.ErrorIs(t, err, cas.ErrFailedPrecondition)
	assert.False(t, got.Worked)
}

type testContext struct {
	uploads  *uploadmocks.MockStore
	objects  *objectmocks.MockStore
	casStore *casmocks.MockStore
	job      *promote.Job
}

func newTestContext(t *testing.T) *testContext {
	t.Helper()

	uploadStore := uploadmocks.NewMockStore(t)
	objectStore := objectmocks.NewMockStore(t)
	casStore := casmocks.NewMockStore(t)
	casService := cas.NewService(cas.ServiceConfig{
		Store:   casStore,
		Objects: objectStore,
	})

	return &testContext{
		uploads:  uploadStore,
		objects:  objectStore,
		casStore: casStore,
		job: promote.New(promote.Config{
			Uploads: uploadStore,
			CAS:     casService,
		}),
	}
}

func uploadSession(uploadID uuid.UUID, digest uploads.Digest, payload []byte, mediaType *string) uploads.Session {
	return uploads.Session{
		ID:                uploadID,
		ExpectedDigest:    digest,
		ExpectedSizeBytes: int64(len(payload)),
		State:             uploads.SessionStateIngesting,
		StagingKey:        uploads.StagingKey(uploadID),
		MediaTypeHint:     mediaType,
	}
}

func digestFor(t *testing.T, payload []byte) uploads.Digest {
	t.Helper()

	sum := sha256.Sum256(payload)
	digest, err := uploads.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	require.NoError(t, err)

	return digest
}

func nopReadCloser(payload []byte) *readCloser {
	return &readCloser{Reader: bytes.NewReader(payload)}
}

type readCloser struct {
	*bytes.Reader
}

func (reader *readCloser) Close() error {
	return nil
}
