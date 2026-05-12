//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAppliesEmbeddedMigrations(t *testing.T) {
	databaseURL := os.Getenv("IMGSRV_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("IMGSRV_TEST_POSTGRES_URL is required")
	}

	ctx := context.Background()
	store := openIntegrationStore(t, ctx, databaseURL)
	version, err := store.SchemaVersion(ctx)

	require.NoError(t, err)
	assert.Equal(t, int64(3), version)

	store = openIntegrationStore(t, ctx, databaseURL)
	version, err = store.SchemaVersion(ctx)

	require.NoError(t, err)
	assert.Equal(t, int64(3), version)
}

func openIntegrationStore(t *testing.T, ctx context.Context, databaseURL string) *Store {
	t.Helper()

	store, err := Open(ctx, Config{URL: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}
