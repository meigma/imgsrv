package auth

import (
	"database/sql"
	"time"

	domain "github.com/meigma/imgsrv/internal/auth"
)

// rowScanner abstracts a pgx row that can scan its columns into destinations.
type rowScanner interface {
	// Scan copies the columns from the matched row into the values pointed at by dest.
	Scan(dest ...any) error
}

// scanToken decodes a single api_tokens row into a domain.Token.
func scanToken(row rowScanner) (domain.Token, error) {
	var token domain.Token
	var lastUsedAt sql.NullTime
	var revokedAt sql.NullTime

	err := row.Scan(
		&token.ID,
		&token.Name,
		&token.TokenPrefix,
		&token.CreatedAt,
		&lastUsedAt,
		&revokedAt,
	)
	if err != nil {
		return domain.Token{}, err
	}

	token.LastUsedAt = optionalTime(lastUsedAt)
	token.RevokedAt = optionalTime(revokedAt)

	return token, nil
}

// optionalTime converts a [sql.NullTime] to a *[time.Time], returning nil when invalid.
func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}
