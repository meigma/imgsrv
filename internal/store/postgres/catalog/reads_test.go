package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

func TestResolvePublishedManifestHeaderPrefersExactPublishedVersion(t *testing.T) {
	imageID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	exactVersionID := uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff")
	aliasVersionID := uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
	queryer := &fakeCatalogQueryer{
		rows: []pgx.Row{
			manifestHeaderRow(imageID, exactVersionID, "latest"),
			manifestHeaderRow(imageID, aliasVersionID, "v1.0.0"),
		},
	}

	got, err := resolvePublishedManifestHeader(context.Background(), queryer, domain.ResolveManifestParams{
		ImageName: "debian",
		Version:   "latest",
	})

	require.NoError(t, err)
	assert.Equal(t, exactVersionID, got.Version.ID)
	assert.Equal(t, "latest", got.Version.Version)
	assert.Equal(t, 1, queryer.queryRowCalls)
}

func TestResolvePublishedManifestHeaderFallsBackToAlias(t *testing.T) {
	imageID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	aliasVersionID := uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
	queryer := &fakeCatalogQueryer{
		rows: []pgx.Row{
			fakeCatalogRow{err: pgx.ErrNoRows},
			manifestHeaderRow(imageID, aliasVersionID, "v1.0.0"),
		},
	}

	got, err := resolvePublishedManifestHeader(context.Background(), queryer, domain.ResolveManifestParams{
		ImageName: "debian",
		Version:   "latest",
	})

	require.NoError(t, err)
	assert.Equal(t, aliasVersionID, got.Version.ID)
	assert.Equal(t, "v1.0.0", got.Version.Version)
	assert.Equal(t, 2, queryer.queryRowCalls)
}

type fakeCatalogQueryer struct {
	rows          []pgx.Row
	queryRowCalls int
}

func (queryer *fakeCatalogQueryer) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected catalog query")
}

func (queryer *fakeCatalogQueryer) QueryRow(context.Context, string, ...any) pgx.Row {
	row := queryer.rows[queryer.queryRowCalls]
	queryer.queryRowCalls++
	return row
}

type fakeCatalogRow struct {
	values []any
	err    error
}

func (row fakeCatalogRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	for i, value := range row.values {
		switch target := dest[i].(type) {
		case *uuid.UUID:
			*target = value.(uuid.UUID)
		case *string:
			*target = value.(string)
		case *domain.VersionState:
			*target = value.(domain.VersionState)
		case *sql.NullString:
			*target = value.(sql.NullString)
		case *sql.NullTime:
			*target = value.(sql.NullTime)
		case *time.Time:
			*target = value.(time.Time)
		}
	}

	return nil
}

func manifestHeaderRow(imageID uuid.UUID, versionID uuid.UUID, version string) pgx.Row {
	createdAt := time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 5, 18, 1, 0, 0, time.UTC)
	publishedAt := time.Date(2026, 5, 5, 18, 2, 0, 0, time.UTC)

	return fakeCatalogRow{
		values: []any{
			imageID,
			"debian",
			sql.NullString{},
			sql.NullString{},
			createdAt,
			updatedAt,
			versionID,
			imageID,
			version,
			domain.VersionStatePublished,
			sql.NullTime{Time: publishedAt, Valid: true},
			createdAt,
			updatedAt,
		},
	}
}
