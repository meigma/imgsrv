package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	authkitpostgres "github.com/meigma/authkit/store/postgres"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	safelog "github.com/meigma/imgsrv/internal/logging"
	"github.com/meigma/imgsrv/internal/materialization/incus"
	"github.com/meigma/imgsrv/internal/publish"
	postgrescatalog "github.com/meigma/imgsrv/internal/store/postgres/catalog"
	postgrespublish "github.com/meigma/imgsrv/internal/store/postgres/publish"
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

	// Logger receives sanitized Postgres adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger
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
	// publish is the durable publish workflow adapter backed by the shared pool.
	publish publish.Store
	// incus is the Incus projection adapter backed by the shared pool.
	incus incus.ProjectionStore
	// uploads is the upload-session adapter backed by the shared pool.
	uploads uploads.Store
	// logger receives sanitized Postgres adapter logs.
	logger *slog.Logger
}

// Open opens Postgres and applies embedded schema migrations.
func Open(ctx context.Context, config Config) (*Store, error) {
	if config.URL == "" {
		return nil, errors.New("postgres url is required")
	}
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "opening postgres store", postgresLogAttrs("postgres.open", config.URL)...)

	pool, openErr := pgxpool.New(ctx, config.URL)
	if openErr != nil {
		return nil, fmt.Errorf("open postgres database: %w", openErr)
	}

	if pingErr := pool.Ping(ctx); pingErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("ping postgres database: %w", pingErr))
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "postgres ping succeeded", postgresLogAttrs("postgres.ping", config.URL)...)

	migrationDB := stdlib.OpenDBFromPool(pool)
	if migrateErr := ApplyMigrations(ctx, migrationDB); migrateErr != nil {
		return nil, closeMigrationAfterError(
			pool,
			migrationDB,
			fmt.Errorf("apply database migrations: %w", migrateErr),
		)
	}
	logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"imgsrv postgres migrations applied",
		postgresLogAttrs("postgres.migrate_imgsrv", config.URL)...)

	if closeErr := migrationDB.Close(); closeErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("close migration database: %w", closeErr))
	}

	if migrateErr := authkitpostgres.Migrate(ctx, pool); migrateErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("apply authkit database migrations: %w", migrateErr))
	}
	logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"authkit postgres migrations applied",
		postgresLogAttrs("postgres.migrate_authkit", config.URL)...)
	authkitStore, authkitErr := authkitpostgres.NewStore(pool)
	if authkitErr != nil {
		return nil, closePoolAfterError(pool, fmt.Errorf("create authkit store: %w", authkitErr))
	}

	uploadStore := postgresuploads.New(pool)
	publishStore := postgrespublish.New(pool)

	return &Store{
		authkit: authkitStore,
		cas:     uploadStore,
		pool:    pool,
		catalog: postgrescatalog.New(pool),
		publish: publishStore,
		incus:   publishStore,
		uploads: uploadStore,
		logger:  logger,
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
	if store.logger != nil {
		store.logger.Debug("postgres store closed", "operation", "postgres.close")
	}
	store.pool = nil
	store.authkit = nil
	store.cas = nil
	store.catalog = nil
	store.publish = nil
	store.incus = nil
	store.uploads = nil

	return nil
}

func postgresLogAttrs(operation string, rawURL string) []slog.Attr {
	attrs := []slog.Attr{slog.String("operation", operation)}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return append(attrs, slog.Bool("url_parse_error", true))
	}
	if parsed.Host != "" {
		attrs = append(attrs, slog.String("host", parsed.Host))
	}
	if database := strings.TrimPrefix(parsed.EscapedPath(), "/"); database != "" {
		attrs = append(attrs, slog.String("database", database))
	}
	if sslMode := parsed.Query().Get("sslmode"); sslMode != "" {
		attrs = append(attrs, slog.String("sslmode", sslMode))
	}

	return attrs
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

// Publish returns the durable publish workflow adapter.
func (store *Store) Publish() publish.Store {
	if store == nil {
		return nil
	}

	return store.publish
}

// IncusProjection returns the Incus projection row adapter.
func (store *Store) IncusProjection() incus.ProjectionStore {
	if store == nil {
		return nil
	}

	return store.incus
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
