package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRequiresURL(t *testing.T) {
	store, err := Open(context.Background(), Config{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres url is required")
	assert.Nil(t, store)
}

func TestApplyMigrationsRequiresDatabase(t *testing.T) {
	err := ApplyMigrations(context.Background(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres database is required")
}

func TestEmbeddedMigrationsAreDiscoverable(t *testing.T) {
	db, err := sql.Open(databaseDriverName, "postgres://user:password@example.invalid/db")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	provider, err := newMigrationProvider(db)

	require.NoError(t, err)
	sources := provider.ListSources()
	require.Len(t, sources, 4)
	assert.Equal(t, int64(1), sources[0].Version)
	assert.Equal(t, "000001_initial_schema.sql", sources[0].Path)
	assert.Equal(t, int64(2), sources[1].Version)
	assert.Equal(t, "000002_allow_raw_gz_artifacts.sql", sources[1].Path)
	assert.Equal(t, int64(3), sources[2].Version)
	assert.Equal(t, "000003_publish_jobs.sql", sources[2].Path)
	assert.Equal(t, int64(4), sources[3].Version)
	assert.Equal(t, "000004_release_artifact_variants.sql", sources[3].Path)
}

func TestSchemaVersionRequiresOpenStore(t *testing.T) {
	version, err := (*Store)(nil).SchemaVersion(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres store is not open")
	assert.Zero(t, version)
}
