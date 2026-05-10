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
				Action:   action,
				Resource: resourceForAction(action),
				Facts:    authz.FactsForAuthentication(authentication),
			}, nil
		})(next)
		handler.ServeHTTP(w, r)
	}
}

func resourceForAction(action string) authkit.Resource {
	if action == authz.ActionAuthManage {
		return authkit.Resource{
			Type: authz.ResourceAuth,
			ID:   authz.ResourceAuth,
		}
	}

	return authkit.Resource{
		Type: authz.ResourceContent,
		ID:   authz.ResourceContent,
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
