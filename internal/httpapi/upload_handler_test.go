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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	httpmocks "github.com/meigma/imgsrv/internal/httpapi/mocks"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestBeginUploadCreatesSession(t *testing.T) {
	tc := newUploadHandlerTestContext(t)
	mediaType := "application/octet-stream"
	filename := "image.qcow2"
	wantSession := uploadSessionFixture(uploads.SessionStateCreated)
	wantSession.MediaTypeHint = &mediaType
	wantSession.FilenameHint = &filename

	tc.uploads.EXPECT().
		BeginUpload(mock.Anything, mock.MatchedBy(func(params uploads.BeginUploadParams) bool {
			return params.ExpectedDigest == digestFixture() &&
				params.ExpectedSizeBytes == 12 &&
				params.MediaTypeHint != nil &&
				*params.MediaTypeHint == mediaType &&
				params.FilenameHint != nil &&
				*params.FilenameHint == filename &&
				params.ExpiresAt.Equal(nowFixture().Add(time.Hour))
		})).
		Return(wantSession, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/uploads", strings.NewReader(`{
		"expected_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"expected_size_bytes": 12,
		"media_type_hint": "application/octet-stream",
		"filename_hint": "image.qcow2"
	}`))
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got uploadSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, uploadIDFixture().String(), got.ID)
	assert.Equal(t, digestFixture().String(), got.ExpectedDigest)
	assert.Equal(t, int64(12), got.ExpectedSizeBytes)
	assert.Equal(t, uploads.SessionStateCreated, got.State)
	assert.Equal(t, expiresFixture().Format(time.RFC3339Nano), got.ExpiresAt)
	require.NotNil(t, got.MediaTypeHint)
	assert.Equal(t, mediaType, *got.MediaTypeHint)
	require.NotNil(t, got.FilenameHint)
	assert.Equal(t, filename, *got.FilenameHint)
}

func TestPutUploadPartStreamsRawBody(t *testing.T) {
	tc := newUploadHandlerTestContext(t)
	body := "hello"

	tc.uploads.EXPECT().
		PutUploadPart(mock.Anything, mock.MatchedBy(func(params uploads.PutUploadPartParams) bool {
			got, err := io.ReadAll(params.Body)
			return err == nil &&
				params.UploadID == uploadIDFixture() &&
				params.PartNumber == 7 &&
				params.SizeBytes == int64(len(body)) &&
				string(got) == body
		})).
		Return(uploads.Part{
			UploadID:   uploadIDFixture(),
			PartNumber: 7,
			ETag:       "etag-7",
			SizeBytes:  int64(len(body)),
		}, nil)

	req := httptest.NewRequest(
		http.MethodPut,
		"/v1/uploads/"+uploadIDFixture().String()+"/parts/7",
		strings.NewReader(body),
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got uploadPartResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, uploadIDFixture().String(), got.UploadID)
	assert.Equal(t, 7, got.PartNumber)
	assert.Equal(t, "etag-7", got.ETag)
	assert.Equal(t, int64(len(body)), got.SizeBytes)
}

func TestGetUploadReturnsSession(t *testing.T) {
	tc := newUploadHandlerTestContext(t)
	mediaType := "application/octet-stream"
	filename := "image.qcow2"
	wantSession := uploadSessionFixture(uploads.SessionStateUploading)
	wantSession.MediaTypeHint = &mediaType
	wantSession.FilenameHint = &filename

	tc.uploads.EXPECT().
		GetUpload(mock.Anything, uploads.GetUploadParams{UploadID: uploadIDFixture()}).
		Return(wantSession, nil)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/uploads/"+uploadIDFixture().String(),
		nil,
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got uploadSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, uploadIDFixture().String(), got.ID)
	assert.Equal(t, digestFixture().String(), got.ExpectedDigest)
	assert.Equal(t, int64(12), got.ExpectedSizeBytes)
	assert.Equal(t, uploads.SessionStateUploading, got.State)
	assert.Equal(t, expiresFixture().Format(time.RFC3339Nano), got.ExpiresAt)
	require.NotNil(t, got.MediaTypeHint)
	assert.Equal(t, mediaType, *got.MediaTypeHint)
	require.NotNil(t, got.FilenameHint)
	assert.Equal(t, filename, *got.FilenameHint)
}

func TestCompleteUploadSubmitsAcceptedPartList(t *testing.T) {
	tc := newUploadHandlerTestContext(t)
	wantSession := uploadSessionFixture(uploads.SessionStateCompleted)

	tc.uploads.EXPECT().
		CompleteUpload(mock.Anything, uploads.CompleteUploadParams{
			UploadID: uploadIDFixture(),
			Parts: []uploads.CompleteUploadPart{
				{Number: 1, ETag: "etag-1", SizeBytes: 5},
				{Number: 2, ETag: "etag-2", SizeBytes: 7},
			},
		}).
		Return(wantSession, nil)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/uploads/"+uploadIDFixture().String()+"/complete",
		strings.NewReader(
			`{"parts":[{"number":1,"etag":"etag-1","size_bytes":5},{"number":2,"etag":"etag-2","size_bytes":7}]}`,
		),
	)
	rec := httptest.NewRecorder()

	tc.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got uploadSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, uploadIDFixture().String(), got.ID)
	assert.Equal(t, uploads.SessionStateCompleted, got.State)
}

func TestUploadHandlersRejectInvalidRequests(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		prepare func(*http.Request)
		want    string
	}{
		{
			name:   "begin rejects malformed JSON",
			method: http.MethodPost,
			path:   "/v1/uploads",
			body:   "{",
			want:   "invalid JSON request body",
		},
		{
			name:   "part rejects invalid upload id",
			method: http.MethodPut,
			path:   "/v1/uploads/not-a-uuid/parts/1",
			body:   "hello",
			want:   "upload id must be a UUID",
		},
		{
			name:   "get rejects invalid upload id",
			method: http.MethodGet,
			path:   "/v1/uploads/not-a-uuid",
			want:   "upload id must be a UUID",
		},
		{
			name:   "part rejects invalid part number",
			method: http.MethodPut,
			path:   "/v1/uploads/" + uploadIDFixture().String() + "/parts/0",
			body:   "hello",
			want:   "part number must be between",
		},
		{
			name:   "part rejects missing content length",
			method: http.MethodPut,
			path:   "/v1/uploads/" + uploadIDFixture().String() + "/parts/1",
			body:   "hello",
			prepare: func(req *http.Request) {
				req.ContentLength = -1
			},
			want: "content length is required",
		},
		{
			name:   "complete rejects malformed JSON",
			method: http.MethodPost,
			path:   "/v1/uploads/" + uploadIDFixture().String() + "/complete",
			body:   "{",
			want:   "invalid JSON request body",
		},
		{
			name:   "complete rejects invalid upload id",
			method: http.MethodPost,
			path:   "/v1/uploads/not-a-uuid/complete",
			body:   `{"parts":[]}`,
			want:   "upload id must be a UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newUploadHandlerTestContext(t)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.prepare != nil {
				tt.prepare(req)
			}
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assertProblem(t, rec, http.StatusBadRequest, tt.want)
		})
	}
}

func TestUploadHandlersMapDomainErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "invalid input", err: uploads.ErrInvalid, wantCode: http.StatusBadRequest},
		{name: "not found", err: uploads.ErrNotFound, wantCode: http.StatusNotFound},
		{name: "conflict", err: uploads.ErrConflict, wantCode: http.StatusConflict},
		{name: "failed precondition", err: uploads.ErrFailedPrecondition, wantCode: http.StatusPreconditionFailed},
		{name: "unexpected", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newUploadHandlerTestContext(t)
			tc.uploads.EXPECT().
				BeginUpload(mock.Anything, mock.Anything).
				Return(uploads.Session{}, tt.err)
			req := httptest.NewRequest(http.MethodPost, "/v1/uploads", strings.NewReader(`{
				"expected_digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				"expected_size_bytes": 12
			}`))
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantCode, rec.Code)
			assertProblem(t, rec, tt.wantCode, tt.err.Error())
		})
	}
}

func TestGetUploadMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "invalid input", err: uploads.ErrInvalid, wantCode: http.StatusBadRequest},
		{name: "not found", err: uploads.ErrNotFound, wantCode: http.StatusNotFound},
		{name: "conflict", err: uploads.ErrConflict, wantCode: http.StatusConflict},
		{name: "failed precondition", err: uploads.ErrFailedPrecondition, wantCode: http.StatusPreconditionFailed},
		{name: "unexpected", err: errors.New("database unavailable"), wantCode: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newUploadHandlerTestContext(t)
			tc.uploads.EXPECT().
				GetUpload(mock.Anything, uploads.GetUploadParams{UploadID: uploadIDFixture()}).
				Return(uploads.Session{}, tt.err)
			req := httptest.NewRequest(http.MethodGet, "/v1/uploads/"+uploadIDFixture().String(), nil)
			rec := httptest.NewRecorder()

			tc.handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantCode, rec.Code)
			assertProblem(t, rec, tt.wantCode, tt.err.Error())
		})
	}
}

func TestUploadHandlersReturnUnavailableWhenServiceMissing(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "begin",
			method: http.MethodPost,
			path:   "/v1/uploads",
			body:   `{}`,
		},
		{
			name:   "get",
			method: http.MethodGet,
			path:   "/v1/uploads/" + uploadIDFixture().String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := New(Dependencies{})
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assertProblem(t, rec, http.StatusServiceUnavailable, errUploadServiceUnavailable.Error())
		})
	}
}

type uploadHandlerTestContext struct {
	uploads *httpmocks.MockUploadService
	handler http.Handler
}

func newUploadHandlerTestContext(t *testing.T) *uploadHandlerTestContext {
	t.Helper()

	uploadService := httpmocks.NewMockUploadService(t)
	return &uploadHandlerTestContext{
		uploads: uploadService,
		handler: New(Dependencies{
			Uploads:   uploadService,
			Now:       nowFixture,
			UploadTTL: time.Hour,
		}),
	}
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, detailContains string) {
	t.Helper()

	assert.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))
	var got problemResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "about:blank", got.Type)
	assert.Equal(t, http.StatusText(status), got.Title)
	assert.Equal(t, status, got.Status)
	assert.Contains(t, got.Detail, detailContains)
}

func uploadIDFixture() uuid.UUID {
	return uuid.MustParse("11111111-2222-3333-4444-555555555555")
}

func digestFixture() uploads.Digest {
	return uploads.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

func nowFixture() time.Time {
	return time.Date(2026, 5, 4, 20, 0, 0, 0, time.UTC)
}

func expiresFixture() time.Time {
	return nowFixture().Add(time.Hour)
}

func uploadSessionFixture(state uploads.SessionState) uploads.Session {
	return uploads.Session{
		ID:                uploadIDFixture(),
		ExpectedDigest:    digestFixture(),
		ExpectedSizeBytes: 12,
		State:             state,
		ExpiresAt:         expiresFixture(),
	}
}
