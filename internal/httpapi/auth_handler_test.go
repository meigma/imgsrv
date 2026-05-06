package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/auth"
	httpmocks "github.com/meigma/imgsrv/internal/httpapi/mocks"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestRequireAuthRejectsMissingServiceFailClosed(t *testing.T) {
	handler := New(Dependencies{})
	req := newHTTPAPIRequest(http.MethodPost, "/v1/uploads", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assertProblem(t, rec, http.StatusServiceUnavailable, errAuthServiceUnavailable.Error())
}

func TestRequireAuthRejectsMissingAndMalformedBearerTokens(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "wrong scheme", authorization: "Basic testtok.secret"},
		{name: "missing token", authorization: "Bearer"},
		{name: "extra fields", authorization: "Bearer testtok.secret extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			authService := httpmocks.NewMockAuthService(t)
			api := &api{auth: authService}
			handler := api.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/uploads", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()

			handler(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, bearerAuthScheme, rec.Header().Get("WWW-Authenticate"))
			assert.False(t, called)
		})
	}
}

func TestRequireAuthRejectsUnknownTokenBeforeCallingNext(t *testing.T) {
	authService := httpmocks.NewMockAuthService(t)
	authService.EXPECT().
		AuthenticateToken(mock.Anything, auth.AuthenticateTokenParams{Token: testBearerToken}).
		Return(auth.Token{}, auth.ErrNotFound)

	called := false
	api := &api{auth: authService}
	handler := api.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := newHTTPAPIRequest(http.MethodPost, "/v1/uploads", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, bearerAuthScheme, rec.Header().Get("WWW-Authenticate"))
	assert.False(t, called)
}

func TestRequireAuthCallsNextForValidToken(t *testing.T) {
	authService := httpmocks.NewMockAuthService(t)
	authService.EXPECT().
		AuthenticateToken(mock.Anything, auth.AuthenticateTokenParams{Token: testBearerToken}).
		Return(auth.Token{ID: uuid.New(), TokenPrefix: "testtok"}, nil)

	called := false
	api := &api{auth: authService}
	handler := api.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := newHTTPAPIRequest(http.MethodPost, "/v1/uploads", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, called)
}

func TestAnonymousReadRouteDoesNotRequireAuthDependency(t *testing.T) {
	uploadService := httpmocks.NewMockUploadService(t)
	uploadService.EXPECT().
		GetUpload(mock.Anything, uploads.GetUploadParams{UploadID: uploadIDFixture()}).
		Return(uploadSessionFixture(uploads.SessionStateCreated), nil)
	handler := New(Dependencies{Uploads: uploadService})
	req := httptest.NewRequest(http.MethodGet, "/v1/uploads/"+uploadIDFixture().String(), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
