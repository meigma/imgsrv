package auth

import (
	"database/sql"
	"time"

	domain "github.com/meigma/imgsrv/internal/auth"
)

type rowScanner interface {
	Scan(dest ...any) error
}

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

func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}
