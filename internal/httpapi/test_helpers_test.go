package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/meigma/imgsrv/internal/auth"
	httpmocks "github.com/meigma/imgsrv/internal/httpapi/mocks"
)

const testBearerToken = "testtok.secret"

// newHTTPAPIRequest creates an HTTP request and authorizes write methods.
func newHTTPAPIRequest(method string, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	authorizeWriteRequest(req)

	return req
}

// authorizeWriteRequest attaches the shared test bearer token to mutating requests.
func authorizeWriteRequest(req *http.Request) {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
		req.Header.Set("Authorization", "Bearer "+testBearerToken)
	}
}

// newAcceptingAuthService returns an auth mock that accepts the shared test bearer token.
func newAcceptingAuthService(t testing.TB) *httpmocks.MockAuthService {
	t.Helper()

	authService := httpmocks.NewMockAuthService(t)
	authService.EXPECT().
		AuthenticateToken(mock.Anything, auth.AuthenticateTokenParams{Token: testBearerToken}).
		Return(auth.Token{
			ID:          uuid.MustParse("11111111-2222-3333-4444-555555555555"),
			Name:        "test",
			TokenPrefix: "testtok",
		}, nil).
		Maybe()

	return authService
}
