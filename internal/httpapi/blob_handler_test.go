package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	httpmocks "github.com/meigma/imgsrv/internal/httpapi/mocks"
	"github.com/meigma/imgsrv/internal/objectstore"
)

const blobPayload = "0123456789"

func TestGetBlobStreamsTrustedBody(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, blobPayload), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
	assert.Equal(t, blobCacheControl, rec.Header().Get("Cache-Control"))
	assert.Equal(t, "application/x-qcow2", rec.Header().Get("Content-Type"))
	assert.Equal(t, blobETag(blob), rec.Header().Get("ETag"))
	assert.Equal(
		t,
		blob.VerifiedAt.UTC().Truncate(time.Second).Format(http.TimeFormat),
		rec.Header().Get("Last-Modified"),
	)
	assert.Equal(t, "10", rec.Header().Get("Content-Length"))
	assert.Equal(t, blobPayload, rec.Body.String())
}

func TestHeadBlobReturnsHeadersWithoutBody(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, blobPayload), nil)

	req := httptest.NewRequest(http.MethodHead, "/v1/blobs/"+blob.Digest.String(), nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "10", rec.Header().Get("Content-Length"))
	assert.Empty(t, rec.Body.String())
}

func TestHeadBlobOpenFailureReturnsError(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(objectstore.ObjectReader{}, errors.New("storage unavailable"))

	req := httptest.NewRequest(http.MethodHead, "/v1/blobs/"+blob.Digest.String(), nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Length"))
	assert.Empty(t, rec.Body.String())
}

func TestGetBlobSupportsExplicitRange(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{
			Digest: blob.Digest,
			Range:  &objectstore.ByteRange{Start: 2, End: 4},
		}).
		Return(blobReaderFixture(blob, "234"), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("Range", "bytes=2-4")
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, "bytes 2-4/10", rec.Header().Get("Content-Range"))
	assert.Equal(t, "3", rec.Header().Get("Content-Length"))
	assert.Equal(t, "234", rec.Body.String())
}

func TestGetBlobSupportsSuffixRange(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{
			Digest: blob.Digest,
			Range:  &objectstore.ByteRange{Start: 6, End: 9},
		}).
		Return(blobReaderFixture(blob, "6789"), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("Range", "bytes=-4")
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, "bytes 6-9/10", rec.Header().Get("Content-Range"))
	assert.Equal(t, "4", rec.Header().Get("Content-Length"))
	assert.Equal(t, "6789", rec.Body.String())
}

func TestGetBlobRejectsUnsupportedMultiRange(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("Range", "bytes=0-1,3-4")
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)
	assert.Equal(t, "bytes */10", rec.Header().Get("Content-Range"))
	assertProblem(t, rec, http.StatusRequestedRangeNotSatisfiable, "multiple ranges are not supported")
}

func TestGetBlobReturnsNotModifiedForMatchingETag(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("If-None-Match", blobETag(blob))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotModified, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestGetBlobReturnsNotModifiedForIfModifiedSince(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("If-Modified-Since", blob.VerifiedAt.Add(time.Hour).UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotModified, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestGetBlobIgnoresRangeWhenIfRangeDoesNotMatch(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, blobPayload), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("Range", "bytes=2-4")
	req.Header.Set("If-Range", `"other"`)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Range"))
	assert.Equal(t, blobPayload, rec.Body.String())
}

func TestGetBlobOpenFailureDoesNotLeakSuccessOnlyLengthHeaders(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("application/x-qcow2")

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{
			Digest: blob.Digest,
			Range:  &objectstore.ByteRange{Start: 2, End: 4},
		}).
		Return(objectstore.ObjectReader{}, errors.New("storage unavailable"))

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	req.Header.Set("Range", "bytes=2-4")
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Range"))
	assert.Empty(t, rec.Header().Get("Content-Length"))
	assertProblem(t, rec, http.StatusInternalServerError, "storage unavailable")
}

func TestGetBlobFallsBackToOctetStream(t *testing.T) {
	tc := newBlobHandlerTestContext(t)
	blob := blobFixture("")
	blob.MediaType = nil

	tc.blobs.EXPECT().
		GetBlob(mock.Anything, cas.GetBlobParams{Digest: blob.Digest}).
		Return(blob, nil)
	tc.blobs.EXPECT().
		OpenBlob(mock.Anything, cas.OpenBlobParams{Digest: blob.Digest}).
		Return(blobReaderFixture(blob, blobPayload), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+blob.Digest.String(), nil)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, blobDefaultContentType, rec.Header().Get("Content-Type"))
}

func TestBlobHandlersReturnUnavailableWhenServiceMissing(t *testing.T) {
	handler := New(Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/"+digestFixture().String(), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assertProblem(t, rec, http.StatusServiceUnavailable, errBlobServiceUnavailable.Error())
}

type blobHandlerTestContext struct {
	blobs   *httpmocks.MockBlobService
	handler http.Handler
}

func newBlobHandlerTestContext(t *testing.T) *blobHandlerTestContext {
	t.Helper()

	blobService := httpmocks.NewMockBlobService(t)
	return &blobHandlerTestContext{
		blobs: blobService,
		handler: New(Dependencies{
			Blobs: blobService,
		}),
	}
}

func blobFixture(mediaType string) cas.Blob {
	digest := digestFixture()
	var mediaTypePtr *string
	if strings.TrimSpace(mediaType) != "" {
		mediaTypePtr = &mediaType
	}

	return cas.Blob{
		Digest:     digest,
		SizeBytes:  int64(len(blobPayload)),
		StorageKey: cas.StorageKey(digest),
		MediaType:  mediaTypePtr,
		VerifiedAt: time.Date(2026, 5, 5, 20, 0, 0, 0, time.UTC),
	}
}

func blobReaderFixture(blob cas.Blob, body string) objectstore.ObjectReader {
	return objectstore.ObjectReader{
		Info: objectstore.ObjectInfo{
			Key:       blob.StorageKey,
			SizeBytes: blob.SizeBytes,
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestWriteBlobProblemSuppressesBodyOnHead(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v1/blobs/"+digestFixture().String(), nil)

	writeBlobProblem(rec, req, http.StatusRequestedRangeNotSatisfiable, "bad range")

	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)
	assert.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))
	assert.Empty(t, rec.Body.String())

	var got problemResponse
	require.Error(t, json.Unmarshal(rec.Body.Bytes(), &got))
}
