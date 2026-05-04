package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // register the database/sql pgx driver
	"github.com/pressly/goose/v3"
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
	db *sql.DB
}

// Open opens Postgres and applies embedded schema migrations.
func Open(ctx context.Context, config Config) (*Store, error) {
	if config.URL == "" {
		return nil, errors.New("postgres url is required")
	}

	db, err := sql.Open(databaseDriverName, config.URL)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, closeAfterError(db, fmt.Errorf("ping postgres database: %w", err))
	}

	if err := ApplyMigrations(ctx, db); err != nil {
		return nil, closeAfterError(db, fmt.Errorf("apply database migrations: %w", err))
	}

	return &Store{db: db}, nil
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
	if store == nil || store.db == nil {
		return nil
	}

	err := store.db.Close()
	store.db = nil

	return err
}

// SchemaVersion returns the current applied Goose schema version.
func (store *Store) SchemaVersion(ctx context.Context) (int64, error) {
	if store == nil || store.db == nil {
		return 0, errors.New("postgres store is not open")
	}

	provider, err := newMigrationProvider(store.db)
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

func closeAfterError(db *sql.DB, err error) error {
	if closeErr := db.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close postgres database: %w", closeErr))
	}

	return err
}
