// Package uploads implements the Postgres upload and CAS ingest adapter.
package uploads

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists upload and CAS ingest operations in Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs an uploads Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) uploadDB() (*pgxpool.Pool, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("postgres uploads store is not open")
	}

	return store.pool, nil
}

func (store *Store) withTx(ctx context.Context, apply func(pgx.Tx) error) error {
	db, err := store.uploadDB()
	if err != nil {
		return err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := apply(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit postgres transaction: %w", err)
	}

	return nil
}
