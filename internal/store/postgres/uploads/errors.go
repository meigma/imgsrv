package uploads

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

const (
	sqlStateCheckViolation      = "23514"
	sqlStateForeignKeyViolation = "23503"
	sqlStateInvalidText         = "22P02"
	sqlStateNotNullViolation    = "23502"
	sqlStateUniqueViolation     = "23505"
)

func mapUploadError(err error) error {
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
