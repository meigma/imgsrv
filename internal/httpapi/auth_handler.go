package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/httpauth"

	"github.com/meigma/imgsrv/internal/authz"
)

const bearerAuthScheme = "Bearer"

var errAuthServiceUnavailable = errors.New("auth service is not configured")

// requireAction authenticates one protected route, authorizes action, and invokes next.
//
//nolint:unparam // Upcoming auth-policy routes will use actions beyond content.write.
func (a *api) requireAction(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.auth == nil {
			writeProblem(w, http.StatusServiceUnavailable, errAuthServiceUnavailable.Error())
			return
		}

		handler := a.auth.RequireAuthorization(func(req *http.Request) (authkit.AuthorizationRequest, error) {
			authentication, ok := httpauth.AuthenticationFromContext(req.Context())
			if !ok {
				return authkit.AuthorizationRequest{}, errors.New("authentication missing from request context")
			}

			return authkit.AuthorizationRequest{
				Action: action,
				Resource: authkit.Resource{
					Type: authz.ResourceContent,
					ID:   authz.ResourceContent,
				},
				Facts: authz.FactsForAuthentication(authentication),
			}, nil
		})(next)
		handler.ServeHTTP(w, r)
	}
}

// WriteAuthError maps authkit failures onto HTTP responses.
func WriteAuthError(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, authkit.ErrUnauthenticated), errors.Is(err, authkit.ErrUnresolvedIdentity):
		writeAuthProblem(w, "invalid bearer token")
	case errors.Is(err, authkit.ErrUnauthorized):
		writeProblem(w, http.StatusForbidden, authErrorDetail(err, authkit.ErrUnauthorized))
	default:
		writeProblem(w, http.StatusInternalServerError, err.Error())
	}
}

// writeAuthProblem writes an authentication challenge with an RFC 9457 body.
func writeAuthProblem(w http.ResponseWriter, detail string) {
	w.Header().Set("WWW-Authenticate", bearerAuthScheme)
	writeProblem(w, http.StatusUnauthorized, detail)
}

func authErrorDetail(err error, sentinel error) string {
	detail := err.Error()
	prefix := sentinel.Error() + ": "
	detail = strings.TrimPrefix(detail, prefix)
	if strings.TrimSpace(detail) == "" || detail == sentinel.Error() {
		return http.StatusText(http.StatusForbidden)
	}

	return detail
}
