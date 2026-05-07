package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/meigma/imgsrv/internal/auth"
)

const bearerAuthScheme = "Bearer"

var errAuthServiceUnavailable = errors.New("auth service is not configured")

// requireAction authenticates one protected route, authorizes action, and invokes next.
//
//nolint:unparam // Upcoming auth-policy routes will use actions beyond content.write.
func (a *api) requireAction(action auth.Action, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := a.auth
		if service == nil {
			writeProblem(w, http.StatusServiceUnavailable, errAuthServiceUnavailable.Error())
			return
		}

		token, ok := bearerToken(r)
		if !ok {
			writeAuthProblem(w, "missing or malformed bearer token")
			return
		}
		principal, err := service.AuthenticateToken(
			r.Context(),
			auth.AuthenticateTokenParams{Token: token},
		)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if !principal.HasAction(action) {
			writeAuthorizationProblem(w, action)
			return
		}

		next(w, r.WithContext(auth.ContextWithPrincipal(r.Context(), principal)))
	}
}

// bearerToken extracts the raw bearer token from Authorization.
func bearerToken(r *http.Request) (string, bool) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], bearerAuthScheme) {
		return "", false
	}
	if strings.TrimSpace(fields[1]) == "" {
		return "", false
	}

	return fields[1], true
}

// writeAuthError maps auth failures onto HTTP responses.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalid), errors.Is(err, auth.ErrNotFound):
		writeAuthProblem(w, "invalid bearer token")
	default:
		writeProblem(w, http.StatusInternalServerError, err.Error())
	}
}

// writeAuthProblem writes an authentication challenge with an RFC 9457 body.
func writeAuthProblem(w http.ResponseWriter, detail string) {
	w.Header().Set("WWW-Authenticate", bearerAuthScheme)
	writeProblem(w, http.StatusUnauthorized, detail)
}

// writeAuthorizationProblem writes an authorization failure for an authenticated principal.
func writeAuthorizationProblem(w http.ResponseWriter, action auth.Action) {
	writeProblem(w, http.StatusForbidden, "principal is not authorized for action "+string(action))
}
