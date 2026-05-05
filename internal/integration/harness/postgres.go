//go:build integration

package harness

import (
	"context"
	"testing"

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

	container, err := tcpostgres.Run(
		ctx,
		postgresImage,
		tcpostgres.WithDatabase(postgresDatabase),
		tcpostgres.WithUsername(postgresUsername),
		tcpostgres.WithPassword(postgresPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return databaseURL
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
