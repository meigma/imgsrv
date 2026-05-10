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

	authkitpostgres "github.com/meigma/authkit/store/postgres"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	postgrescatalog "github.com/meigma/imgsrv/internal/store/postgres/catalog"
	postgresuploads "github.com/meigma/imgsrv/internal/store/postgres/uploads"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	// databaseDriverName is the database/sql driver name registered by pgx.
	databaseDriverName = "pgx"
	// migrationTableName is the Goose schema-migration tracking table.
	migrationTableName = "imgsrv_schema_migrations"
	// migrationsPath is the embedded filesystem subdirectory holding migration SQL files.
	migrationsPath = "migrations"
	// postgresMigrationLockID is the advisory-lock key used to serialize schema migrations across processes.
	postgresMigrationLockID = int64(0x696d677372763201)
)

// embeddedMigrations is the embedded Goose migration filesystem applied at store open.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Config configures the Postgres store.
type Config struct {
	// URL is the PostgreSQL connection URL.
	URL string
}

// Store owns the Postgres database connection.
type Store struct {
	// authkit is the authkit adapter backed by the shared pool.
	authkit *authkitpostgres.Store
	// cas is the CAS blob and ingest-job adapter backed by the shared pool.
	cas cas.Store
	// pool is the underlying pgx connection pool shared by all adapters.
	pool *pgxpool.Pool
	// catalog is the image catalog adapter backed by the shared pool.
	catalog catalog.Store
	// uploads is the upload-session adapter backed by the shared pool.
	uploads uploads.Store
}

// Open opens Postgres and applies embedded schema migrations.
func Open(ctx context.Context, config Config) (*Store, error) {
	if config.URL == "" {
		return nil, errors.New("postgres url is required")
	}

	pool, openErr := pgxpool.New(ctx, config.URL)
	if openErr != nil {
		return nil, fmt.Errorf("open postgres database: %w", openErr)
	}

	if pingErr := pool.Ping(ctx); pingErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("ping postgres database: %w", pingErr))
	}

	migrationDB := stdlib.OpenDBFromPool(pool)
	if migrateErr := ApplyMigrations(ctx, migrationDB); migrateErr != nil {
		return nil, closeMigrationAfterError(
			pool,
			migrationDB,
			fmt.Errorf("apply database migrations: %w", migrateErr),
		)
	}

	if closeErr := migrationDB.Close(); closeErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("close migration database: %w", closeErr))
	}

	if migrateErr := authkitpostgres.Migrate(ctx, pool); migrateErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("apply authkit database migrations: %w", migrateErr))
	}
	authkitStore, authkitErr := authkitpostgres.NewStore(pool)
	if authkitErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("create authkit store: %w", authkitErr))
	}

	uploadStore := postgresuploads.New(pool)

	return &Store{
		authkit: authkitStore,
		cas:     uploadStore,
		pool:    pool,
		catalog: postgrescatalog.New(pool),
		uploads: uploadStore,
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
	store.authkit = nil
	store.cas = nil
	store.catalog = nil
	store.uploads = nil

	return nil
}

// Authkit returns the authkit Postgres adapter.
func (store *Store) Authkit() *authkitpostgres.Store {
	if store == nil {
		return nil
	}

	return store.authkit
}

// Catalog returns the image catalog adapter.
func (store *Store) Catalog() catalog.Store {
	if store == nil {
		return nil
	}

	return store.catalog
}

// CAS returns the CAS blob and ingest adapter.
func (store *Store) CAS() cas.Store {
	if store == nil {
		return nil
	}

	return store.cas
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

// newMigrationProvider builds a Goose provider rooted at the embedded migration filesystem.
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

// withMigrationLock runs apply while holding the Postgres advisory migration lock.
//
// The lock serializes concurrent migration attempts across processes and is released even
// when apply returns an error or the context that opened the connection is cancelled.
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

// closePoolAfterError closes pool and returns the original err for the caller to propagate.
func closePoolAfterError(pool *pgxpool.Pool, err error) error {
	pool.Close()

	return err
}

// closeMigrationAfterError closes the migration database and pool, joining any close error onto err.
func closeMigrationAfterError(pool *pgxpool.Pool, db *sql.DB, err error) error {
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close migration database: %w", closeErr))
	}
	pool.Close()

	return err
}
