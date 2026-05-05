// Package catalog implements the Postgres image catalog adapter.
package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists image catalog operations in Postgres.
type Store struct {
	// pool is the pgx connection pool used for all catalog queries.
	pool *pgxpool.Pool
}

// New constructs a catalog Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// catalogDB returns the underlying pgx pool, or an error when the store has
// not been constructed with one.
func (store *Store) catalogDB() (*pgxpool.Pool, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("postgres catalog store is not open")
	}

	return store.pool, nil
}

// withTx runs apply inside a Postgres transaction, committing on success and
// rolling back on any error returned by apply or by the commit itself.
func (store *Store) withTx(ctx context.Context, apply func(pgx.Tx) error) error {
	db, err := store.catalogDB()
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
