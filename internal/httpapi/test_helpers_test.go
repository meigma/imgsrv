package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/meigma/authkit"
	"github.com/meigma/authkit/httpauth"
	"github.com/stretchr/testify/require"
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

// newAcceptingAuthService returns auth middleware that accepts the shared test bearer token.
func newAcceptingAuthService(t testing.TB) *httpauth.Middleware {
	t.Helper()

	return newTestAuthMiddleware(t, true)
}

func newDenyingAuthService(t testing.TB) *httpauth.Middleware {
	t.Helper()

	return newTestAuthMiddleware(t, false)
}

func newTestAuthMiddleware(t testing.TB, allowed bool) *httpauth.Middleware {
	t.Helper()

	pipeline, err := authkit.NewPipeline(authkit.PipelineOptions{
		Authenticators: []authkit.Authenticator{testAuthenticator{}},
		Resolver:       testResolver{},
		Authorizer:     testAuthorizer{allowed: allowed},
	})
	require.NoError(t, err)
	middleware, err := httpauth.NewMiddleware(pipeline, httpauth.WithErrorRenderer(WriteAuthError))
	require.NoError(t, err)

	return middleware
}

type testAuthenticator struct{}

func (testAuthenticator) Name() string {
	return "test"
}

func (testAuthenticator) Authenticate(_ context.Context, req *http.Request) (*authkit.Identity, error) {
	fields := strings.Fields(req.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], bearerAuthScheme) || fields[1] != testBearerToken {
		return nil, authkit.ErrUnauthenticated
	}

	return &authkit.Identity{
		Provider: "test",
		Subject:  "subject-1",
	}, nil
}

type testResolver struct{}

func (testResolver) ResolveIdentity(
	_ context.Context,
	identity authkit.Identity,
) (*authkit.Principal, error) {
	if identity.Provider == "" || identity.Subject == "" {
		return nil, authkit.ErrUnresolvedIdentity
	}

	return &authkit.Principal{
		ID:   uuid.MustParse("11111111-2222-3333-4444-555555555555").String(),
		Kind: authkit.PrincipalKindService,
	}, nil
}

type testAuthorizer struct {
	allowed bool
}

func (a testAuthorizer) Can(_ context.Context, check authkit.AuthorizationCheck) (authkit.Decision, error) {
	if check.Action == "" {
		return authkit.Decision{}, errors.New("action is required")
	}
	if !a.allowed {
		return authkit.Decision{
			Allowed: false,
			Reason:  "principal is not authorized for action " + check.Action,
		}, nil
	}

	return authkit.Decision{Allowed: true}, nil
}
