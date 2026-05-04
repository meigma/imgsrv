// Package auth implements the Postgres API-token auth adapter.
package auth

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists API-token authentication state in Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs an auth Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) authDB() (*pgxpool.Pool, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("postgres auth store is not open")
	}

	return store.pool, nil
}
