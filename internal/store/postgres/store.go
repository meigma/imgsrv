package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/meigma/imgsrv/internal/auth"
	"github.com/meigma/imgsrv/internal/catalog"
	postgresauth "github.com/meigma/imgsrv/internal/store/postgres/auth"
	postgrescatalog "github.com/meigma/imgsrv/internal/store/postgres/catalog"
	postgresuploads "github.com/meigma/imgsrv/internal/store/postgres/uploads"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	databaseDriverName      = "pgx"
	migrationTableName      = "imgsrv_schema_migrations"
	migrationsPath          = "migrations"
	postgresMigrationLockID = int64(0x696d677372763201)
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Config configures the Postgres store.
type Config struct {
	// URL is the PostgreSQL connection URL.
	URL string
}

// Store owns the Postgres database connection.
type Store struct {
	auth    auth.Store
	pool    *pgxpool.Pool
	catalog catalog.Store
	uploads uploads.Store
}

// Open opens Postgres and applies embedded schema migrations.
func Open(ctx context.Context, config Config) (*Store, error) {
	if config.URL == "" {
		return nil, errors.New("postgres url is required")
	}

	pool, err := pgxpool.New(ctx, config.URL)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("ping postgres database: %w", err))
	}

	migrationDB := stdlib.OpenDBFromPool(pool)
	if err := ApplyMigrations(ctx, migrationDB); err != nil {
		return nil, closeMigrationAfterError(
			pool,
			migrationDB,
			fmt.Errorf("apply database migrations: %w", err),
		)
	}

	if err := migrationDB.Close(); err != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("close migration database: %w", err))
	}

	return &Store{
		auth:    postgresauth.New(pool),
		pool:    pool,
		catalog: postgrescatalog.New(pool),
		uploads: postgresuploads.New(pool),
	}, nil
}

// ApplyMigrations applies all embedded Goose migrations to db.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("postgres database is required")
	}

	provider, err := newMigrationProvider(db)
	if err != nil {
		return err
	}

	return withMigrationLock(ctx, db, func() error {
		if _, err := provider.Up(ctx); err != nil {
			return fmt.Errorf("run goose migrations: %w", err)
		}

		return nil
	})
}

// Close releases the Postgres database connection.
func (store *Store) Close() error {
	if store == nil || store.pool == nil {
		return nil
	}

	store.pool.Close()
	store.pool = nil
	store.auth = nil
	store.catalog = nil
	store.uploads = nil

	return nil
}

// Auth returns the API-token auth adapter.
func (store *Store) Auth() auth.Store {
	if store == nil {
		return nil
	}

	return store.auth
}

// Catalog returns the image catalog adapter.
func (store *Store) Catalog() catalog.Store {
	if store == nil {
		return nil
	}

	return store.catalog
}

// Uploads returns the upload and CAS ingest adapter.
func (store *Store) Uploads() uploads.Store {
	if store == nil {
		return nil
	}

	return store.uploads
}

// SchemaVersion returns the current applied Goose schema version.
func (store *Store) SchemaVersion(ctx context.Context) (int64, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("postgres store is not open")
	}

	migrationDB := stdlib.OpenDBFromPool(store.pool)
	defer func() {
		_ = migrationDB.Close()
	}()

	provider, err := newMigrationProvider(migrationDB)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}

	return version, nil
}

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("postgres database is required")
	}

	migrationFS, err := fs.Sub(embeddedMigrations, migrationsPath)
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrationFS,
		goose.WithTableName(migrationTableName),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return nil, fmt.Errorf("create goose migration provider: %w", err)
	}

	return provider, nil
}

func withMigrationLock(ctx context.Context, db *sql.DB, apply func() error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer func() {
		closeErr := conn.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close migration lock connection: %w", closeErr)
		}
	}()

	if _, lockErr := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, postgresMigrationLockID); lockErr != nil {
		return fmt.Errorf("acquire postgres migration lock: %w", lockErr)
	}
	defer func() {
		_, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, postgresMigrationLockID)
		if err == nil && unlockErr != nil {
			err = fmt.Errorf("release postgres migration lock: %w", unlockErr)
		}
	}()

	return apply()
}

func closePoolAfterError(pool *pgxpool.Pool, err error) error {
	pool.Close()

	return err
}

func closeMigrationAfterError(pool *pgxpool.Pool, db *sql.DB, err error) error {
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close migration database: %w", closeErr))
	}
	pool.Close()

	return err
}
