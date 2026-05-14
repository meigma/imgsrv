package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appmetrics "github.com/meigma/imgsrv/internal/metrics"
	"github.com/meigma/imgsrv/internal/telemetry"
)

func TestInstrumentStoreRecordsOperationsOutcomesAndBytes(t *testing.T) {
	ctx := context.Background()
	providers, recorder := newObjectstoreMetricsRecorder(t)
	inner := &fakeStore{
		putPart: func(context.Context, PutPartParams) (Part, error) {
			return Part{Number: 1, ETag: "etag-1", SizeBytes: 5}, nil
		},
		openObject: func(context.Context, OpenObjectParams) (ObjectReader, error) {
			return ObjectReader{
				Info: ObjectInfo{Key: "objects/test", SizeBytes: 10},
				Body: io.NopCloser(bytes.NewReader([]byte("abcdefghij"))),
			}, nil
		},
		statObject: func(context.Context, StatObjectParams) (ObjectInfo, error) {
			return ObjectInfo{}, ErrNotFound
		},
		copyObject: func(context.Context, CopyObjectParams) (ObjectInfo, error) {
			return ObjectInfo{}, errors.Join(ErrAlreadyExists, errors.New("exists"))
		},
	}
	store := InstrumentStore(inner, recorder)

	part, err := store.PutPart(ctx, PutPartParams{
		Key:        "objects/test",
		UploadID:   "upload-1",
		PartNumber: 1,
		Body:       bytes.NewReader([]byte("hello")),
		SizeBytes:  5,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), part.SizeBytes)
	reader, err := store.OpenObject(ctx, OpenObjectParams{
		Key:   "objects/test",
		Range: &ByteRange{Start: 2, End: 4},
	})
	require.NoError(t, err)
	require.NoError(t, reader.Body.Close())
	_, err = store.StatObject(ctx, StatObjectParams{Key: "objects/missing"})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.CopyObject(ctx, CopyObjectParams{
		SourceKey:      "objects/test",
		DestinationKey: "objects/copy",
	})
	require.ErrorIs(t, err, ErrAlreadyExists)

	body := scrapeObjectstoreMetrics(t, providers)

	assert.Contains(t, body, `operation="put_part"`)
	assert.Contains(t, body, `operation="open_object"`)
	assert.Contains(t, body, `operation="stat_object"`)
	assert.Contains(t, body, `outcome="not_found"`)
	assert.Contains(t, body, `outcome="already_exists"`)
	assert.Contains(t, body, `direction="write"`)
	assert.Contains(t, body, `direction="read"`)
	assert.Contains(t, body, "imgsrv_objectstore_bytes_total")
	assert.Contains(t, body, " 5\n")
	assert.Contains(t, body, " 3\n")
}

func TestInstrumentStoreReturnsUnwrappedStoreWhenMetricsDisabled(t *testing.T) {
	inner := &fakeStore{}

	got := InstrumentStore(inner, appmetrics.Noop())

	assert.Same(t, inner, got)
}

func newObjectstoreMetricsRecorder(t *testing.T) (*telemetry.Telemetry, *appmetrics.Recorder) {
	t.Helper()

	providers, err := telemetry.New(telemetry.Config{MetricsPath: "/metrics"})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, providers.Shutdown(context.Background()))
	})
	recorder, err := appmetrics.New(providers.Meter("github.com/meigma/imgsrv/internal/objectstore_test"))
	require.NoError(t, err)

	return providers, recorder
}

func scrapeObjectstoreMetrics(t *testing.T, providers *telemetry.Telemetry) string {
	t.Helper()

	rec := httptest.NewRecorder()
	providers.MetricsHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	return rec.Body.String()
}

type fakeStore struct {
	createMultipartUpload   func(context.Context, CreateMultipartUploadParams) (MultipartUpload, error)
	putPart                 func(context.Context, PutPartParams) (Part, error)
	completeMultipartUpload func(context.Context, CompleteMultipartUploadParams) (ObjectInfo, error)
	abortMultipartUpload    func(context.Context, AbortMultipartUploadParams) error
	openObject              func(context.Context, OpenObjectParams) (ObjectReader, error)
	statObject              func(context.Context, StatObjectParams) (ObjectInfo, error)
	copyObject              func(context.Context, CopyObjectParams) (ObjectInfo, error)
	deleteObject            func(context.Context, DeleteObjectParams) error
}

func (store *fakeStore) CreateMultipartUpload(ctx context.Context, params CreateMultipartUploadParams) (MultipartUpload, error) {
	return store.createMultipartUpload(ctx, params)
}

func (store *fakeStore) PutPart(ctx context.Context, params PutPartParams) (Part, error) {
	return store.putPart(ctx, params)
}

func (store *fakeStore) CompleteMultipartUpload(ctx context.Context, params CompleteMultipartUploadParams) (ObjectInfo, error) {
	return store.completeMultipartUpload(ctx, params)
}

func (store *fakeStore) AbortMultipartUpload(ctx context.Context, params AbortMultipartUploadParams) error {
	return store.abortMultipartUpload(ctx, params)
}

func (store *fakeStore) OpenObject(ctx context.Context, params OpenObjectParams) (ObjectReader, error) {
	return store.openObject(ctx, params)
}

func (store *fakeStore) StatObject(ctx context.Context, params StatObjectParams) (ObjectInfo, error) {
	return store.statObject(ctx, params)
}

func (store *fakeStore) CopyObject(ctx context.Context, params CopyObjectParams) (ObjectInfo, error) {
	return store.copyObject(ctx, params)
}

func (store *fakeStore) DeleteObject(ctx context.Context, params DeleteObjectParams) error {
	return store.deleteObject(ctx, params)
}
