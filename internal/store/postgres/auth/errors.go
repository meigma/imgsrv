package auth

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/meigma/imgsrv/internal/auth"
)

const (
	// sqlStateCheckViolation is the Postgres SQLSTATE for check_violation.
	sqlStateCheckViolation = "23514"

	// sqlStateForeignKeyViolation is the Postgres SQLSTATE for foreign_key_violation.
	sqlStateForeignKeyViolation = "23503"

	// sqlStateInvalidText is the Postgres SQLSTATE for invalid_text_representation.
	sqlStateInvalidText = "22P02"

	// sqlStateNotNullViolation is the Postgres SQLSTATE for not_null_violation.
	sqlStateNotNullViolation = "23502"

	// sqlStateUniqueViolation is the Postgres SQLSTATE for unique_violation.
	sqlStateUniqueViolation = "23505"
)

// mapAuthError translates a pgx or pgconn error into a domain auth error.
func mapAuthError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.ConstraintName)
	case sqlStateForeignKeyViolation:
		return fmt.Errorf("%w: %s", domain.ErrNotFound, pgErr.Message)
	case sqlStateCheckViolation:
		return fmt.Errorf("%w: %s", domain.ErrFailedPrecondition, pgErr.Message)
	case sqlStateInvalidText, sqlStateNotNullViolation:
		return fmt.Errorf("%w: %s", domain.ErrInvalid, pgErr.Message)
	default:
		return err
	}
}
