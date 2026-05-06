package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/meigma/imgsrv/internal/auth"
)

const bearerAuthScheme = "Bearer"

var errAuthServiceUnavailable = errors.New("auth service is not configured")

// requireAuth authenticates one write route before invoking next.
func (a *api) requireAuth(next http.HandlerFunc) http.HandlerFunc {
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
		if _, err := service.AuthenticateToken(r.Context(), auth.AuthenticateTokenParams{Token: token}); err != nil {
			writeAuthError(w, err)
			return
		}

		next(w, r)
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
