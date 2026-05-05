package catalog

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

const (
	// sqlStateCheckViolation is the Postgres SQLSTATE for a check-constraint failure.
	sqlStateCheckViolation = "23514"

	// sqlStateForeignKeyViolation is the Postgres SQLSTATE for a foreign-key violation.
	sqlStateForeignKeyViolation = "23503"

	// sqlStateInvalidText is the Postgres SQLSTATE for invalid text representation.
	sqlStateInvalidText = "22P02"

	// sqlStateNotNullViolation is the Postgres SQLSTATE for a not-null constraint violation.
	sqlStateNotNullViolation = "23502"

	// sqlStateUniqueViolation is the Postgres SQLSTATE for a unique-constraint violation.
	sqlStateUniqueViolation = "23505"
)

// mapCatalogError translates a Postgres driver error into the matching catalog
// domain error sentinel, returning the original error when no mapping applies.
func mapCatalogError(err error) error {
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
