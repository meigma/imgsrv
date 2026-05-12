package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleStreamsRoutesServeAnonymousJSON(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{
			name:     "index",
			path:     "/streams/v1/index.json",
			wantBody: `{"format":"index:1.0"}` + "\n",
		},
		{
			name:     "product file",
			path:     "/streams/v1/images.json",
			wantBody: `{"format":"products:1.0"}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := New(Dependencies{
				Auth: newDenyingAuthService(t),
				SimpleStreams: fakeSimpleStreamsService{
					index:       []byte(`{"format":"index:1.0"}` + "\n"),
					productFile: []byte(`{"format":"products:1.0"}` + "\n"),
				},
			})
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestSimpleStreamsRoutesReturnUnavailableWhenServiceMissing(t *testing.T) {
	handler := New(Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "incus simplestreams service is not configured")
}

func TestSimpleStreamsRoutesReturnInternalErrorWhenProjectionFails(t *testing.T) {
	handler := New(Dependencies{
		SimpleStreams: fakeSimpleStreamsService{err: errors.New("projection failed")},
	})
	req := httptest.NewRequest(http.MethodGet, "/streams/v1/images.json", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "projection failed")
}

type fakeSimpleStreamsService struct {
	index       []byte
	productFile []byte
	err         error
}

func (fake fakeSimpleStreamsService) Index(context.Context) ([]byte, error) {
	if fake.err != nil {
		return nil, fake.err
	}

	return fake.index, nil
}

func (fake fakeSimpleStreamsService) ProductFile(context.Context) ([]byte, error) {
	if fake.err != nil {
		return nil, fake.err
	}

	return fake.productFile, nil
}
