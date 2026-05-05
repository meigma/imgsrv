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
	postgresImage    = "postgres:16-alpine"
	postgresDatabase = "imgsrv"
	postgresUsername = "imgsrv"
	postgresPassword = "imgsrv"
)

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

func openStore(ctx context.Context, t testing.TB, postgresURL string) *postgres.Store {
	t.Helper()

	store, err := postgres.Open(ctx, postgres.Config{URL: postgresURL})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}
