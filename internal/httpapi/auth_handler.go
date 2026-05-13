package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/meigma/authkit"
	"github.com/meigma/authkit/httpauth"

	"github.com/meigma/imgsrv/internal/authz"
	safelog "github.com/meigma/imgsrv/internal/logging"
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

		resource := resourceForAction(action)
		handler := a.auth.RequireAuthorization(func(req *http.Request) (authkit.AuthorizationRequest, error) {
			authentication, ok := httpauth.AuthenticationFromContext(req.Context())
			if !ok {
				return authkit.AuthorizationRequest{}, errors.New("authentication missing from request context")
			}

			return authkit.AuthorizationRequest{
				Action:   action,
				Resource: resource,
				Facts:    authz.FactsForAuthentication(authentication),
			}, nil
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.logAuthorizationAllowed(r, action, resource)
			next(w, r)
		}))
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

// NewAuthErrorRenderer returns an authkit error renderer that logs sanitized auth failures.
func NewAuthErrorRenderer(logger *slog.Logger) httpauth.ErrorRenderer {
	if logger == nil {
		logger = safelog.Nop()
	}

	return func(w http.ResponseWriter, r *http.Request, err error) {
		logAuthError(logger, r, err)
		WriteAuthError(w, r, err)
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

func (a *api) logAuthorizationAllowed(r *http.Request, action string, resource authkit.Resource) {
	if a.logger == nil {
		return
	}

	attrs := authorizationLogAttrs(r, action, resource, "")
	a.logger.LogAttrs(r.Context(), slog.LevelDebug, "authorization allowed", attrs...)
}

func logAuthError(logger *slog.Logger, r *http.Request, err error) {
	if logger == nil || r == nil {
		return
	}

	level := authErrorLogLevel(err)
	attrs := authorizationLogAttrs(r, "", authkit.Resource{}, authErrorKind(err))
	if level >= slog.LevelError {
		attrs = append(attrs, slog.Any("error", err))
	}
	logger.LogAttrs(r.Context(), level, "authorization denied", attrs...)
}

func authorizationLogAttrs(
	r *http.Request,
	action string,
	resource authkit.Resource,
	errorKind string,
) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("operation", "authorize"),
		slog.String("request_id", RequestIDFromContext(r.Context())),
	}
	if action != "" {
		attrs = append(attrs, slog.String("action", action))
	}
	if resource.Type != "" {
		attrs = append(attrs, slog.String("resource_type", resource.Type))
	}
	if resource.ID != "" {
		attrs = append(attrs, slog.String("resource_id", resource.ID))
	}
	if errorKind != "" {
		attrs = append(attrs, slog.String("error_kind", errorKind))
	}
	if authentication, ok := httpauth.AuthenticationFromContext(r.Context()); ok {
		attrs = append(attrs,
			slog.String("authenticator", authentication.AuthenticatorName),
			slog.String("principal_id", authentication.Principal.ID),
			slog.String("identity_provider", authentication.Identity.Provider),
			slog.String(
				"subject_hash",
				safelog.SubjectHash(authentication.Identity.Provider, authentication.Identity.Subject),
			),
		)
	}

	return attrs
}

func authErrorLogLevel(err error) slog.Level {
	switch {
	case errors.Is(err, authkit.ErrUnauthorized):
		return slog.LevelWarn
	case errors.Is(err, authkit.ErrUnauthenticated), errors.Is(err, authkit.ErrUnresolvedIdentity):
		return slog.LevelDebug
	default:
		return slog.LevelError
	}
}

func authErrorKind(err error) string {
	switch {
	case errors.Is(err, authkit.ErrUnauthenticated):
		return "unauthenticated"
	case errors.Is(err, authkit.ErrUnresolvedIdentity):
		return "unresolved_identity"
	case errors.Is(err, authkit.ErrUnauthorized):
		return "unauthorized"
	default:
		return "internal"
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
