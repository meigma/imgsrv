// Package auth defines authentication persistence boundaries.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Error identifies a category of authentication persistence failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrConflict means the requested auth identity already exists.
	ErrConflict Error = "auth conflict"

	// ErrFailedPrecondition means the operation violates auth state.
	ErrFailedPrecondition Error = "auth failed precondition"

	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "auth invalid input"

	// ErrNotFound means the requested auth resource does not exist.
	ErrNotFound Error = "auth not found"
)

// PrincipalKind identifies the authentication mechanism that produced a principal.
type PrincipalKind string

const (
	// PrincipalKindAPIToken identifies principals authenticated by stored API tokens.
	PrincipalKindAPIToken PrincipalKind = "api_token"

	// PrincipalKindOIDC identifies principals authenticated by a configured OIDC issuer.
	PrincipalKindOIDC PrincipalKind = "oidc"

	// PrincipalKindGitHubActions identifies principals authenticated by GitHub Actions OIDC.
	PrincipalKindGitHubActions PrincipalKind = "github_actions"
)

// Action identifies an operation an authenticated principal may perform.
type Action string

const (
	// ActionContentWrite permits upload, draft, publish, and alias mutation operations.
	ActionContentWrite Action = "content.write"

	// ActionAuthManage permits authentication policy management operations.
	ActionAuthManage Action = "auth.manage"
)

// Principal is an authenticated caller plus the actions granted to it.
type Principal struct {
	// Kind identifies the authentication mechanism that produced the principal.
	Kind PrincipalKind

	// ID is the mechanism-scoped stable principal identifier.
	ID string

	// Actions are the operations this principal may perform.
	Actions []Action
}

// Authenticator authenticates one bearer-token format.
type Authenticator interface {
	// AuthenticateToken validates a raw bearer token and returns the resulting principal.
	AuthenticateToken(context.Context, AuthenticateTokenParams) (Principal, error)
}

// HasAction reports whether principal has action.
func (principal Principal) HasAction(action Action) bool {
	return slices.Contains(principal.Actions, action)
}

type principalContextKey struct{}

// ContextWithPrincipal stores principal in ctx for downstream handlers.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated principal stored in ctx.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

// Store persists API-token authentication state.
type Store interface {
	// CreateToken stores API-token metadata for a pre-generated raw token.
	CreateToken(context.Context, CreateTokenParams) (Token, error)

	// LookupActiveToken returns a non-revoked token matching the supplied prefix and hash.
	LookupActiveToken(context.Context, LookupActiveTokenParams) (Token, error)

	// MarkTokenUsed records successful use of a token.
	MarkTokenUsed(context.Context, MarkTokenUsedParams) (Token, error)

	// RevokeToken marks a token revoked.
	RevokeToken(context.Context, RevokeTokenParams) (Token, error)
}

// CreateTokenParams creates an API token record from non-secret token metadata.
type CreateTokenParams struct {
	// ID is the stable token identity to persist.
	ID uuid.UUID

	// Name is the operator-facing token name.
	Name string

	// TokenPrefix is the non-secret token prefix used for lookup.
	TokenPrefix string

	// TokenHash is the derived hash of the full raw token.
	TokenHash string
}

// Token is API-token metadata safe to return from auth persistence.
type Token struct {
	// ID is the stable token identity.
	ID uuid.UUID

	// Name is the operator-facing token name.
	Name string

	// TokenPrefix is the non-secret token prefix used for lookup.
	TokenPrefix string

	// CreatedAt is when the token record was created.
	CreatedAt time.Time

	// LastUsedAt is set when the token is successfully used.
	LastUsedAt *time.Time

	// RevokedAt is set when the token is revoked.
	RevokedAt *time.Time
}

// LookupActiveTokenParams looks up a non-revoked token by prefix and hash.
type LookupActiveTokenParams struct {
	// TokenPrefix is the non-secret token prefix used for lookup.
	TokenPrefix string

	// TokenHash is the derived token hash to match.
	TokenHash string
}

// AuthenticateTokenParams authenticates a raw bearer token.
type AuthenticateTokenParams struct {
	// Token is the raw bearer token value after removing the Authorization scheme.
	Token string
}

// MarkTokenUsedParams records successful token use.
type MarkTokenUsedParams struct {
	// ID identifies the token.
	ID uuid.UUID
}

// RevokeTokenParams revokes a token.
type RevokeTokenParams struct {
	// ID identifies the token.
	ID uuid.UUID
}

// ValidateTokenPrefix validates a non-secret API token prefix.
func ValidateTokenPrefix(prefix string) error {
	pattern := `^[A-Za-z0-9_-]{6,64}$`
	if !matches(pattern, prefix) {
		return fmt.Errorf("%w: token prefix must match %s", ErrInvalid, pattern)
	}

	return nil
}

// ParseTokenPrefix returns the non-secret prefix from a raw API token.
func ParseTokenPrefix(token string) (string, error) {
	token = strings.TrimSpace(token)
	prefix, secret, ok := strings.Cut(token, ".")
	if !ok || strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%w: token must match <prefix>.<secret>", ErrInvalid)
	}
	if err := ValidateTokenPrefix(prefix); err != nil {
		return "", err
	}

	return prefix, nil
}

// HashToken returns the sha256 digest of a full raw API token.
func HashToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("%w: token is required", ErrInvalid)
	}
	sum := sha256.Sum256([]byte(token))

	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ValidateTokenHash validates the stored hash representation of a raw API token.
func ValidateTokenHash(hash string) error {
	pattern := `^sha256:[0-9a-f]{64}$`
	if !matches(pattern, hash) {
		return fmt.Errorf("%w: token hash must match %s", ErrInvalid, pattern)
	}

	return nil
}

// ValidateRequiredText validates non-empty text after trimming ASCII whitespace.
func ValidateRequiredText(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}

	return nil
}

// ValidateTokenID validates a token identity.
func ValidateTokenID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: token id is required", ErrInvalid)
	}

	return nil
}

// matches reports whether value matches the supplied regular expression pattern.
func matches(pattern string, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}
