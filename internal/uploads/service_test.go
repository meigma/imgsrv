package uploads_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/objectstore"
	objectmocks "github.com/meigma/imgsrv/internal/objectstore/mocks"
	"github.com/meigma/imgsrv/internal/uploads"
	uploadmocks "github.com/meigma/imgsrv/internal/uploads/mocks"
)

var (
	testUploadID = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	testDigest   = uploads.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	testNow      = time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC)
	testExpires  = time.Date(2026, 5, 4, 17, 0, 0, 0, time.UTC)
)

const testStorageUploadID = "storage-upload-1"

type serviceTestContext struct {
	store   *uploadmocks.MockStore
	objects *objectmocks.MockStore
	service *uploads.Service
}

func newServiceTestContext(t *testing.T) *serviceTestContext {
	t.Helper()

	store := uploadmocks.NewMockStore(t)
	objects := objectmocks.NewMockStore(t)

	return &serviceTestContext{
		store:   store,
		objects: objects,
		service: uploads.NewService(uploads.ServiceConfig{
			Store:   store,
			Objects: objects,
			Now: func() time.Time {
				return testNow
			},
		}),
	}
}

func TestServiceBeginUploadCreatesObjectstoreUploadThenSession(t *testing.T) {
	tc := newServiceTestContext(t)
	ctx := context.Background()

	var stagingKey string
	tc.objects.EXPECT().
		CreateMultipartUpload(mock.Anything, mock.MatchedBy(func(params objectstore.CreateMultipartUploadParams) bool {
			stagingKey = params.Key
			return strings.HasPrefix(params.Key, "staging/uploads/") &&
				params.Key != uploads.StagingKey(uuid.Nil)
		})).
		Return(objectstore.MultipartUpload{UploadID: testStorageUploadID}, nil)
	tc.store.EXPECT().
		CreateSession(mock.Anything, mock.MatchedBy(func(params uploads.CreateSessionParams) bool {
			return params.ID != uuid.Nil &&
				params.ExpectedDigest == testDigest &&
				params.ExpectedSizeBytes == 12 &&
				params.StorageUploadID == testStorageUploadID &&
				params.ExpiresAt.Equal(testExpires) &&
				uploads.StagingKey(params.ID) == stagingKey
		})).
		RunAndReturn(func(_ context.Context, params uploads.CreateSessionParams) (uploads.Session, error) {
			return uploadSession(params.ID, uploads.SessionStateCreated, params.ExpectedSizeBytes), nil
		})

	got, err := tc.service.BeginUpload(ctx, uploads.BeginUploadParams{
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		ExpiresAt:         testExpires,
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, got.ID)
	assert.Equal(t, uploads.StagingKey(got.ID), got.StagingKey)
	assert.Equal(t, testStorageUploadID, got.StorageUploadID)
}

func TestServiceBeginUploadAbortsObjectstoreUploadWhenSessionCreateFails(t *testing.T) {
	tc := newServiceTestContext(t)
	storeErr := errors.New("create session failed")

	tc.objects.EXPECT().
		CreateMultipartUpload(mock.Anything, objectstore.CreateMultipartUploadParams{
			Key: uploads.StagingKey(testUploadID),
		}).
		Return(objectstore.MultipartUpload{UploadID: testStorageUploadID}, nil)
	tc.store.EXPECT().
		CreateSession(mock.Anything, uploads.CreateSessionParams{
			ID:                testUploadID,
			ExpectedDigest:    testDigest,
			ExpectedSizeBytes: 12,
			StorageUploadID:   testStorageUploadID,
			ExpiresAt:         testExpires,
		}).
		Return(uploads.Session{}, storeErr)
	tc.objects.EXPECT().
		AbortMultipartUpload(mock.Anything, objectstore.AbortMultipartUploadParams{
			Key:      uploads.StagingKey(testUploadID),
			UploadID: testStorageUploadID,
		}).
		Return(nil)

	got, err := tc.service.BeginUpload(context.Background(), uploads.BeginUploadParams{
		ID:                testUploadID,
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		ExpiresAt:         testExpires,
	})

	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, got)
}

func TestServiceBeginUploadRejectsInvalidInput(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.BeginUpload(context.Background(), uploads.BeginUploadParams{
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
	})

	require.ErrorIs(t, err, uploads.ErrInvalid)
	assert.Empty(t, got)
}

func TestServiceBeginUploadRejectsExpiredInput(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.BeginUpload(context.Background(), uploads.BeginUploadParams{
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		ExpiresAt:         testNow,
	})

	require.ErrorIs(t, err, uploads.ErrInvalid)
	assert.Empty(t, got)
}

func TestServiceBeginUploadRejectsMissingDependencies(t *testing.T) {
	params := validBeginUploadParams()

	service := uploads.NewService(uploads.ServiceConfig{})
	got, err := service.BeginUpload(context.Background(), params)
	require.Error(t, err)
	require.NotErrorIs(t, err, uploads.ErrInvalid)
	assert.Contains(t, err.Error(), "uploads store")
	assert.Empty(t, got)

	service = uploads.NewService(uploads.ServiceConfig{
		Store: uploadmocks.NewMockStore(t),
	})
	got, err = service.BeginUpload(context.Background(), params)
	require.Error(t, err)
	require.NotErrorIs(t, err, uploads.ErrInvalid)
	assert.Contains(t, err.Error(), "object store")
	assert.Empty(t, got)
}

func TestServicePutUploadPartStoresObjectPartThenDurablePart(t *testing.T) {
	tc := newServiceTestContext(t)
	body := strings.NewReader("hello")
	session := uploadSession(testUploadID, uploads.SessionStateCreated, 5)
	wantPart := uploads.Part{
		UploadID:   testUploadID,
		PartNumber: 1,
		ETag:       "etag-1",
		SizeBytes:  5,
	}

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		PutPart(mock.Anything, mock.MatchedBy(func(params objectstore.PutPartParams) bool {
			return params.Key == session.StagingKey &&
				params.UploadID == session.StorageUploadID &&
				params.PartNumber == 1 &&
				params.Body == body &&
				params.SizeBytes == 5
		})).
		Return(objectstore.Part{Number: 1, ETag: "etag-1", SizeBytes: 5}, nil)
	tc.store.EXPECT().
		PutPart(mock.Anything, uploads.PutPartParams{
			UploadID:   testUploadID,
			PartNumber: 1,
			ETag:       "etag-1",
			SizeBytes:  5,
		}).
		Return(wantPart, nil)

	got, err := tc.service.PutUploadPart(context.Background(), uploads.PutUploadPartParams{
		UploadID:   testUploadID,
		PartNumber: 1,
		Body:       body,
		SizeBytes:  5,
	})

	require.NoError(t, err)
	assert.Equal(t, wantPart, got)
}

func TestServicePutUploadPartRejectsTerminalStatesBeforeObjectstore(t *testing.T) {
	tests := []struct {
		name  string
		state uploads.SessionState
	}{
		{name: "completed", state: uploads.SessionStateCompleted},
		{name: "ingesting", state: uploads.SessionStateIngesting},
		{name: "ready", state: uploads.SessionStateReady},
		{name: "failed", state: uploads.SessionStateFailed},
		{name: "aborted", state: uploads.SessionStateAborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newServiceTestContext(t)
			tc.store.EXPECT().
				GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
				Return(uploadSession(testUploadID, tt.state, 5), nil)

			got, err := tc.service.PutUploadPart(context.Background(), uploads.PutUploadPartParams{
				UploadID:   testUploadID,
				PartNumber: 1,
				Body:       strings.NewReader("hello"),
				SizeBytes:  5,
			})

			require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
			assert.Empty(t, got)
		})
	}
}

func TestServicePutUploadPartRejectsInvalidInputBeforeStore(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.PutUploadPart(context.Background(), uploads.PutUploadPartParams{
		PartNumber: 1,
		Body:       strings.NewReader("hello"),
		SizeBytes:  5,
	})

	require.ErrorIs(t, err, uploads.ErrInvalid)
	assert.Empty(t, got)
}

func TestServicePutUploadPartRejectsExpiredSessionBeforeObjectstore(t *testing.T) {
	tc := newServiceTestContext(t)
	session := uploadSession(testUploadID, uploads.SessionStateUploading, 5)
	session.ExpiresAt = testNow
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)

	got, err := tc.service.PutUploadPart(context.Background(), uploads.PutUploadPartParams{
		UploadID:   testUploadID,
		PartNumber: 1,
		Body:       strings.NewReader("hello"),
		SizeBytes:  5,
	})

	require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCompleteUploadCompletesObjectstoreThenDurableState(t *testing.T) {
	tc := newServiceTestContext(t)
	sizeBytes := objectstore.MultipartMinPartSizeBytes + 3
	session := uploadSession(testUploadID, uploads.SessionStateUploading, sizeBytes)
	wantSession := uploadSession(testUploadID, uploads.SessionStateCompleted, sizeBytes)
	completeParts := []objectstore.CompletePart{
		{Number: 1, ETag: "etag-1", SizeBytes: objectstore.MultipartMinPartSizeBytes},
		{Number: 2, ETag: "etag-2", SizeBytes: 3},
	}

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		CompleteMultipartUpload(mock.Anything, objectstore.CompleteMultipartUploadParams{
			Key:      session.StagingKey,
			UploadID: session.StorageUploadID,
			Parts:    completeParts,
		}).
		Return(objectstore.ObjectInfo{Key: session.StagingKey, SizeBytes: sizeBytes}, nil)
	tc.store.EXPECT().
		CompleteSession(mock.Anything, uploads.CompleteSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
		UploadID: testUploadID,
		Parts: []uploads.CompleteUploadPart{
			{Number: 2, ETag: "etag-2", SizeBytes: 3},
			{Number: 1, ETag: "etag-1", SizeBytes: objectstore.MultipartMinPartSizeBytes},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceCompleteUploadRecoversCompletedStagingObjectAfterMissingMultipartUpload(t *testing.T) {
	tc := newServiceTestContext(t)
	sizeBytes := objectstore.MultipartMinPartSizeBytes + 3
	session := uploadSession(testUploadID, uploads.SessionStateUploading, sizeBytes)
	wantSession := uploadSession(testUploadID, uploads.SessionStateCompleted, sizeBytes)

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		CompleteMultipartUpload(mock.Anything, mock.Anything).
		Return(objectstore.ObjectInfo{}, objectNotFoundErr())
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: session.StagingKey}).
		Return(objectstore.ObjectInfo{Key: session.StagingKey, SizeBytes: sizeBytes}, nil)
	tc.store.EXPECT().
		CompleteSession(mock.Anything, uploads.CompleteSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
		UploadID: testUploadID,
		Parts: []uploads.CompleteUploadPart{
			{Number: 1, ETag: "etag-1", SizeBytes: objectstore.MultipartMinPartSizeBytes},
			{Number: 2, ETag: "etag-2", SizeBytes: 3},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceCompleteUploadReturnsCurrentStateWithoutObjectstore(t *testing.T) {
	tests := []struct {
		name  string
		state uploads.SessionState
	}{
		{name: "completed", state: uploads.SessionStateCompleted},
		{name: "ingesting", state: uploads.SessionStateIngesting},
		{name: "ready", state: uploads.SessionStateReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newServiceTestContext(t)
			wantSession := uploadSession(testUploadID, tt.state, 12)
			tc.store.EXPECT().
				GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
				Return(wantSession, nil)

			got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
				UploadID: testUploadID,
			})

			require.NoError(t, err)
			assert.Equal(t, wantSession, got)
		})
	}
}

func TestServiceCompleteUploadRejectsSizeMismatchBeforeObjectstore(t *testing.T) {
	tc := newServiceTestContext(t)
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(uploadSession(testUploadID, uploads.SessionStateUploading, 10), nil)

	got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
		UploadID: testUploadID,
		Parts: []uploads.CompleteUploadPart{
			{Number: 1, ETag: "etag-1", SizeBytes: 12},
		},
	})

	require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCompleteUploadRejectsExpiredSessionBeforeObjectstoreCompletion(t *testing.T) {
	tc := newServiceTestContext(t)
	session := uploadSession(testUploadID, uploads.SessionStateUploading, 12)
	session.ExpiresAt = testNow
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: session.StagingKey}).
		Return(objectstore.ObjectInfo{}, objectNotFoundErr())

	got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
		UploadID: testUploadID,
		Parts: []uploads.CompleteUploadPart{
			{Number: 1, ETag: "etag-1", SizeBytes: 12},
		},
	})

	require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceCompleteUploadRejectsInvalidPartsBeforeObjectstore(t *testing.T) {
	tc := newServiceTestContext(t)
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(uploadSession(testUploadID, uploads.SessionStateUploading, 10), nil)

	got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
		UploadID: testUploadID,
	})

	require.ErrorIs(t, err, uploads.ErrInvalid)
	assert.Empty(t, got)
}

func TestServiceCompleteUploadRejectsFailedAndAbortedStates(t *testing.T) {
	tests := []struct {
		name  string
		state uploads.SessionState
	}{
		{name: "failed", state: uploads.SessionStateFailed},
		{name: "aborted", state: uploads.SessionStateAborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newServiceTestContext(t)
			tc.store.EXPECT().
				GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
				Return(uploadSession(testUploadID, tt.state, 12), nil)

			got, err := tc.service.CompleteUpload(context.Background(), uploads.CompleteUploadParams{
				UploadID: testUploadID,
			})

			require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
			assert.Empty(t, got)
		})
	}
}

func TestServiceAbortUploadAbortsObjectstoreThenDurableState(t *testing.T) {
	tc := newServiceTestContext(t)
	session := uploadSession(testUploadID, uploads.SessionStateUploading, 12)
	wantSession := uploadSession(testUploadID, uploads.SessionStateAborted, 12)

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		AbortMultipartUpload(mock.Anything, objectstore.AbortMultipartUploadParams{
			Key:      session.StagingKey,
			UploadID: session.StorageUploadID,
		}).
		Return(nil)
	tc.store.EXPECT().
		AbortSession(mock.Anything, uploads.AbortSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.AbortUpload(context.Background(), uploads.AbortUploadParams{
		UploadID: testUploadID,
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceAbortUploadTreatsMissingObjectstoreUploadAsAlreadyAbsent(t *testing.T) {
	tc := newServiceTestContext(t)
	session := uploadSession(testUploadID, uploads.SessionStateCreated, 12)
	wantSession := uploadSession(testUploadID, uploads.SessionStateAborted, 12)

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		AbortMultipartUpload(mock.Anything, objectstore.AbortMultipartUploadParams{
			Key:      session.StagingKey,
			UploadID: session.StorageUploadID,
		}).
		Return(objectNotFoundErr())
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: session.StagingKey}).
		Return(objectstore.ObjectInfo{}, objectNotFoundErr())
	tc.store.EXPECT().
		AbortSession(mock.Anything, uploads.AbortSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.AbortUpload(context.Background(), uploads.AbortUploadParams{
		UploadID: testUploadID,
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceAbortUploadCompletesSessionWhenMissingMultipartHasStagedObject(t *testing.T) {
	tc := newServiceTestContext(t)
	session := uploadSession(testUploadID, uploads.SessionStateCreated, 12)
	wantSession := uploadSession(testUploadID, uploads.SessionStateCompleted, 12)

	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(session, nil)
	tc.objects.EXPECT().
		AbortMultipartUpload(mock.Anything, objectstore.AbortMultipartUploadParams{
			Key:      session.StagingKey,
			UploadID: session.StorageUploadID,
		}).
		Return(objectNotFoundErr())
	tc.objects.EXPECT().
		StatObject(mock.Anything, objectstore.StatObjectParams{Key: session.StagingKey}).
		Return(objectstore.ObjectInfo{Key: session.StagingKey, SizeBytes: 12}, nil)
	tc.store.EXPECT().
		CompleteSession(mock.Anything, uploads.CompleteSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.AbortUpload(context.Background(), uploads.AbortUploadParams{
		UploadID: testUploadID,
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceAbortUploadReturnsAlreadyAbortedSession(t *testing.T) {
	tc := newServiceTestContext(t)
	wantSession := uploadSession(testUploadID, uploads.SessionStateAborted, 12)
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.AbortUpload(context.Background(), uploads.AbortUploadParams{
		UploadID: testUploadID,
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceAbortUploadRejectsNonMutableStateBeforeObjectstore(t *testing.T) {
	tc := newServiceTestContext(t)
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(uploadSession(testUploadID, uploads.SessionStateCompleted, 12), nil)

	got, err := tc.service.AbortUpload(context.Background(), uploads.AbortUploadParams{
		UploadID: testUploadID,
	})

	require.ErrorIs(t, err, uploads.ErrFailedPrecondition)
	assert.Empty(t, got)
}

func TestServiceGetUploadDelegatesToStore(t *testing.T) {
	tc := newServiceTestContext(t)
	wantSession := uploadSession(testUploadID, uploads.SessionStateUploading, 12)
	tc.store.EXPECT().
		GetSession(mock.Anything, uploads.GetSessionParams{ID: testUploadID}).
		Return(wantSession, nil)

	got, err := tc.service.GetUpload(context.Background(), uploads.GetUploadParams{
		UploadID: testUploadID,
	})

	require.NoError(t, err)
	assert.Equal(t, wantSession, got)
}

func TestServiceGetUploadRejectsInvalidInputBeforeStore(t *testing.T) {
	tc := newServiceTestContext(t)

	got, err := tc.service.GetUpload(context.Background(), uploads.GetUploadParams{})

	require.ErrorIs(t, err, uploads.ErrInvalid)
	assert.Empty(t, got)
}

func uploadSession(id uuid.UUID, state uploads.SessionState, sizeBytes int64) uploads.Session {
	return uploads.Session{
		ID:                id,
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: sizeBytes,
		State:             state,
		StorageUploadID:   testStorageUploadID,
		StagingKey:        uploads.StagingKey(id),
		ExpiresAt:         testExpires,
	}
}

func validBeginUploadParams() uploads.BeginUploadParams {
	return uploads.BeginUploadParams{
		ID:                testUploadID,
		ExpectedDigest:    testDigest,
		ExpectedSizeBytes: 12,
		ExpiresAt:         testExpires,
	}
}

func objectNotFoundErr() error {
	return errors.Join(objectstore.ErrNotFound, errors.New("missing multipart upload"))
}
