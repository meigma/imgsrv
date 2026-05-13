//go:build integration

package harness

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/meigma/imgsrv/internal/store/postgres"
)

const (
	// postgresImage is the Postgres container image used for the integration
	// harness.
	postgresImage = "postgres:16-alpine"

	// postgresDatabase is the database name created inside the container.
	postgresDatabase = "imgsrv"

	// postgresUsername is the role the harness connects as.
	postgresUsername = "imgsrv"

	// postgresPassword is the static password seeded for the test role.
	postgresPassword = "imgsrv"
)

// startPostgres launches a disposable Postgres container for the harness and
// returns its connection string with sslmode disabled.
func startPostgres(ctx context.Context, t testing.TB) string {
	t.Helper()

	databaseURL, container, err := startPostgresContainer(ctx)
	if container != nil {
		testcontainers.CleanupContainer(t, container)
	}
	require.NoError(t, err)

	return databaseURL
}

func startPostgresContainer(ctx context.Context) (string, testcontainers.Container, error) {
	container, err := tcpostgres.Run(
		ctx,
		postgresImage,
		tcpostgres.WithDatabase(postgresDatabase),
		tcpostgres.WithUsername(postgresUsername),
		tcpostgres.WithPassword(postgresPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", container, fmt.Errorf("start postgres container: %w", err)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", container, fmt.Errorf("read postgres connection string: %w", err)
	}

	return databaseURL, container, nil
}

// openStore opens the migrated Postgres store against postgresURL and
// registers a cleanup that closes the store at test teardown.
func openStore(ctx context.Context, t testing.TB, postgresURL string) *postgres.Store {
	t.Helper()

	store, err := postgres.Open(ctx, postgres.Config{URL: postgresURL})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}

func openIsolatedStore(ctx context.Context, t testing.TB, basePostgresURL string, schema string) *postgres.Store {
	t.Helper()

	require.NoError(t, createPostgresSchema(ctx, basePostgresURL, schema))
	return openStore(ctx, t, postgresURLWithSearchPath(t, basePostgresURL, schema))
}

func createPostgresSchema(ctx context.Context, postgresURL string, schema string) error {
	pool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		return fmt.Errorf("open postgres schema bootstrap pool: %w", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if err != nil {
		return fmt.Errorf("create postgres schema %q: %w", schema, err)
	}

	return nil
}

func postgresURLWithSearchPath(t testing.TB, postgresURL string, schema string) string {
	t.Helper()

	parsed, err := url.Parse(postgresURL)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}
