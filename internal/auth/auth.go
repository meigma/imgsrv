// Package auth defines authentication persistence boundaries.
package auth

import (
	"context"
	"fmt"
	"regexp"
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

// Store persists API-token authentication state.
type Store interface {
	// LookupActiveToken returns a non-revoked token matching the supplied prefix and hash.
	LookupActiveToken(context.Context, LookupActiveTokenParams) (Token, error)

	// MarkTokenUsed records successful use of a token.
	MarkTokenUsed(context.Context, MarkTokenUsedParams) (Token, error)

	// RevokeToken marks a token revoked.
	RevokeToken(context.Context, RevokeTokenParams) (Token, error)
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

func matches(pattern string, value string) bool {
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}
