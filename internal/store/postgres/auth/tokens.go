package auth

import (
	"context"

	domain "github.com/meigma/imgsrv/internal/auth"
)

// CreateToken stores API-token metadata for a pre-generated raw token.
func (store *Store) CreateToken(ctx context.Context, params domain.CreateTokenParams) (domain.Token, error) {
	if err := validateCreateTokenParams(params); err != nil {
		return domain.Token{}, err
	}

	db, err := store.authDB()
	if err != nil {
		return domain.Token{}, err
	}

	token, err := scanToken(db.QueryRow(
		ctx,
		`INSERT INTO api_tokens (id, name, token_prefix, token_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING `+tokenColumns,
		params.ID,
		params.Name,
		params.TokenPrefix,
		params.TokenHash,
	))
	if err != nil {
		return domain.Token{}, mapAuthError(err)
	}

	return token, nil
}

// LookupActiveToken looks up a non-revoked token by prefix and hash.
func (store *Store) LookupActiveToken(
	ctx context.Context,
	params domain.LookupActiveTokenParams,
) (domain.Token, error) {
	if err := validateLookupActiveTokenParams(params); err != nil {
		return domain.Token{}, err
	}

	db, err := store.authDB()
	if err != nil {
		return domain.Token{}, err
	}

	token, err := scanToken(db.QueryRow(
		ctx,
		`SELECT `+tokenColumns+`
		FROM api_tokens
		WHERE token_prefix = $1
			AND token_hash = $2
			AND revoked_at IS NULL`,
		params.TokenPrefix,
		params.TokenHash,
	))
	if err != nil {
		return domain.Token{}, mapAuthError(err)
	}

	return token, nil
}

// MarkTokenUsed records successful token use.
func (store *Store) MarkTokenUsed(
	ctx context.Context,
	params domain.MarkTokenUsedParams,
) (domain.Token, error) {
	if err := validateMarkTokenUsedParams(params); err != nil {
		return domain.Token{}, err
	}

	db, err := store.authDB()
	if err != nil {
		return domain.Token{}, err
	}

	token, err := scanToken(db.QueryRow(
		ctx,
		`UPDATE api_tokens
		SET last_used_at = now()
		WHERE id = $1
			AND revoked_at IS NULL
		RETURNING `+tokenColumns,
		params.ID,
	))
	if err != nil {
		return domain.Token{}, mapAuthError(err)
	}

	return token, nil
}

// RevokeToken revokes a token.
func (store *Store) RevokeToken(ctx context.Context, params domain.RevokeTokenParams) (domain.Token, error) {
	if err := validateRevokeTokenParams(params); err != nil {
		return domain.Token{}, err
	}

	db, err := store.authDB()
	if err != nil {
		return domain.Token{}, err
	}

	token, err := scanToken(db.QueryRow(
		ctx,
		`UPDATE api_tokens
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1
		RETURNING `+tokenColumns,
		params.ID,
	))
	if err != nil {
		return domain.Token{}, mapAuthError(err)
	}

	return token, nil
}
