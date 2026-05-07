//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/meigma/imgsrv/internal/cas"
	domain "github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestCatalogBrowsingReadsImagesAndVersions(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()

	images, err := catalogStore.ListImages(ctx, domain.ListImagesParams{})
	require.NoError(t, err)
	assert.Empty(t, images)

	createdImages := make(map[string]domain.Image)
	for _, name := range []string{"ubuntu", "alpine", "debian"} {
		image, err := catalogStore.CreateImage(ctx, domain.CreateImageParams{Name: name})
		require.NoError(t, err)
		createdImages[name] = image
	}

	images, err = catalogStore.ListImages(ctx, domain.ListImagesParams{})
	require.NoError(t, err)
	assert.Empty(t, images)

	_, err = catalogStore.GetImage(ctx, domain.GetImageParams{Name: "debian"})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.ListVersions(ctx, domain.ListVersionsParams{ImageName: "debian"})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.GetImage(ctx, domain.GetImageParams{Name: "missing"})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	baseCreatedAt := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	insertDraftCatalogVersion(t, store, createdImages["ubuntu"].ID, "v1.0.0", baseCreatedAt.Add(2*time.Hour))
	insertDraftCatalogVersion(t, store, createdImages["debian"].ID, "v2.0.0", baseCreatedAt.Add(2*time.Hour))
	insertPublishedCatalogVersion(t, store, createdImages["debian"].ID, "v1.0.0", baseCreatedAt)
	insertPublishedCatalogVersion(t, store, createdImages["debian"].ID, "v3.0.0", baseCreatedAt.Add(time.Hour))
	insertPublishedCatalogVersion(t, store, createdImages["alpine"].ID, "v1.0.0", baseCreatedAt)

	images, err = catalogStore.ListImages(ctx, domain.ListImagesParams{})
	require.NoError(t, err)
	require.Len(t, images, 2)
	assert.Equal(t, []string{"alpine", "debian"}, imageNames(images))

	debian, err := catalogStore.GetImage(ctx, domain.GetImageParams{Name: "debian"})
	require.NoError(t, err)
	assert.Equal(t, "debian", debian.Name)

	versions, err := catalogStore.ListVersions(ctx, domain.ListVersionsParams{ImageName: "debian"})
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, []string{"v3.0.0", "v1.0.0"}, versionNames(versions))

	_, err = catalogStore.ListVersions(ctx, domain.ListVersionsParams{ImageName: "ubuntu"})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.ListVersions(ctx, domain.ListVersionsParams{ImageName: "missing"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCatalogRejectsDirectPublishedVersionInsert(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()
	image := createImage(t, ctx, catalogStore, "direct-published-insert")

	_, err := store.pool.Exec(
		ctx,
		`INSERT INTO image_versions (id, image_id, version, state, published_at)
		VALUES ($1, $2, $3, 'published', now())`,
		uuid.New(),
		image.ID,
		"v1.0.0",
	)
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.Message, "image_versions must be inserted as drafts")
}

func TestCatalogDraftDeletesArePathScopedAndDraftOnly(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()

	createImage(t, ctx, catalogStore, "draft-delete")
	createImage(t, ctx, catalogStore, "other-delete")
	createVersion(t, ctx, catalogStore, "draft-delete", "v1.0.0")
	createVersion(t, ctx, catalogStore, "other-delete", "v1.0.0")

	primaryDigest := catalogDigest("1")
	attachmentDigest := catalogDigest("2")
	artifact := createArtifact(t, ctx, catalogStore, "draft-delete", "v1.0.0", primaryDigest)
	otherArtifact := createArtifact(t, ctx, catalogStore, "other-delete", "v1.0.0", primaryDigest)
	attachment := createAttachment(
		t,
		ctx,
		catalogStore,
		"draft-delete",
		"v1.0.0",
		artifact.ID,
		"checksums",
		attachmentDigest,
	)
	otherAttachment := createAttachment(
		t,
		ctx,
		catalogStore,
		"draft-delete",
		"v1.0.0",
		artifact.ID,
		"signature",
		attachmentDigest,
	)

	err := catalogStore.DeleteArtifact(ctx, domain.DeleteArtifactParams{
		ImageName:  "draft-delete",
		Version:    "v1.0.0",
		ArtifactID: otherArtifact.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	err = catalogStore.DeleteAttachment(ctx, domain.DeleteAttachmentParams{
		ImageName:    "other-delete",
		Version:      "v1.0.0",
		ArtifactID:   artifact.ID,
		AttachmentID: attachment.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	err = catalogStore.DeleteAttachment(ctx, domain.DeleteAttachmentParams{
		ImageName:    "draft-delete",
		Version:      "v1.0.0",
		ArtifactID:   artifact.ID,
		AttachmentID: attachment.ID,
	})
	require.NoError(t, err)

	manifest, err := catalogStore.GetVersionManifest(ctx, domain.GetVersionManifestParams{
		ImageName: "draft-delete",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 1)
	assert.Equal(t, []domain.Attachment{otherAttachment}, manifest.Artifacts[0].Attachments)

	err = catalogStore.DeleteArtifact(ctx, domain.DeleteArtifactParams{
		ImageName:  "draft-delete",
		Version:    "v1.0.0",
		ArtifactID: artifact.ID,
	})
	require.NoError(t, err)
	assertNoAttachments(t, store, artifact.ID)

	manifest, err = catalogStore.GetVersionManifest(ctx, domain.GetVersionManifestParams{
		ImageName: "draft-delete",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	assert.Empty(t, manifest.Artifacts)

	createImage(t, ctx, catalogStore, "published-delete")
	createVersion(t, ctx, catalogStore, "published-delete", "v1.0.0")
	publishedPrimaryDigest := catalogDigest("3")
	publishedAttachmentDigest := catalogDigest("4")
	insertTrustedBlob(t, store, publishedPrimaryDigest, 4096)
	insertTrustedBlob(t, store, publishedAttachmentDigest, 256)
	publishedArtifact := createArtifact(t, ctx, catalogStore, "published-delete", "v1.0.0", publishedPrimaryDigest)
	publishedAttachment := createAttachment(
		t,
		ctx,
		catalogStore,
		"published-delete",
		"v1.0.0",
		publishedArtifact.ID,
		"checksums",
		publishedAttachmentDigest,
	)
	_, err = catalogStore.PublishVersion(ctx, domain.PublishVersionParams{
		ImageName: "published-delete",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)

	err = catalogStore.DeleteAttachment(ctx, domain.DeleteAttachmentParams{
		ImageName:    "published-delete",
		Version:      "v1.0.0",
		ArtifactID:   publishedArtifact.ID,
		AttachmentID: publishedAttachment.ID,
	})
	assert.ErrorIs(t, err, domain.ErrFailedPrecondition)

	err = catalogStore.DeleteArtifact(ctx, domain.DeleteArtifactParams{
		ImageName:  "published-delete",
		Version:    "v1.0.0",
		ArtifactID: publishedArtifact.ID,
	})
	assert.ErrorIs(t, err, domain.ErrFailedPrecondition)
}

func TestCatalogPublishedArtifactReadsArePublishedAndPathScoped(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()

	createImage(t, ctx, catalogStore, "published-artifacts")
	createVersion(t, ctx, catalogStore, "published-artifacts", "v1.0.0")
	createVersion(t, ctx, catalogStore, "published-artifacts", "draft-only")

	primaryDigest := catalogDigest("5")
	attachmentDigest := catalogDigest("6")
	foreignPrimaryDigest := catalogDigest("7")
	foreignAttachmentDigest := catalogDigest("8")
	insertTrustedBlob(t, store, primaryDigest, 4096)
	insertTrustedBlob(t, store, attachmentDigest, 256)
	insertTrustedBlob(t, store, foreignPrimaryDigest, 2048)
	insertTrustedBlob(t, store, foreignAttachmentDigest, 256)

	publishedArtifact := createArtifact(t, ctx, catalogStore, "published-artifacts", "v1.0.0", primaryDigest)
	publishedAttachment := createAttachment(
		t,
		ctx,
		catalogStore,
		"published-artifacts",
		"v1.0.0",
		publishedArtifact.ID,
		"checksums",
		attachmentDigest,
	)
	foreignArtifact := createVariantArtifact(
		t,
		ctx,
		catalogStore,
		"published-artifacts",
		"v1.0.0",
		foreignPrimaryDigest,
		"arm64",
	)
	foreignAttachment := createAttachment(
		t,
		ctx,
		catalogStore,
		"published-artifacts",
		"v1.0.0",
		foreignArtifact.ID,
		"signature",
		foreignAttachmentDigest,
	)
	draftArtifact := createArtifact(t, ctx, catalogStore, "published-artifacts", "draft-only", primaryDigest)
	_, err := catalogStore.PublishVersion(ctx, domain.PublishVersionParams{
		ImageName: "published-artifacts",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)

	artifacts, err := catalogStore.ListPublishedArtifacts(ctx, domain.ListPublishedArtifactsParams{
		ImageName: "published-artifacts",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	assert.Equal(t, []uuid.UUID{publishedArtifact.ID, foreignArtifact.ID}, artifactIDs(artifacts))

	gotArtifact, err := catalogStore.GetPublishedArtifact(ctx, domain.GetPublishedArtifactParams{
		ImageName:  "published-artifacts",
		Version:    "v1.0.0",
		ArtifactID: publishedArtifact.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, publishedArtifact.ID, gotArtifact.ID)

	gotAttachment, err := catalogStore.GetPublishedAttachment(ctx, domain.GetPublishedAttachmentParams{
		ImageName:    "published-artifacts",
		Version:      "v1.0.0",
		ArtifactID:   publishedArtifact.ID,
		AttachmentID: publishedAttachment.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, publishedAttachment.ID, gotAttachment.ID)

	_, err = catalogStore.ListPublishedArtifacts(ctx, domain.ListPublishedArtifactsParams{
		ImageName: "published-artifacts",
		Version:   "draft-only",
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.GetPublishedArtifact(ctx, domain.GetPublishedArtifactParams{
		ImageName:  "published-artifacts",
		Version:    "draft-only",
		ArtifactID: draftArtifact.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.GetPublishedArtifact(ctx, domain.GetPublishedArtifactParams{
		ImageName:  "published-artifacts",
		Version:    "v1.0.0",
		ArtifactID: draftArtifact.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = catalogStore.GetPublishedAttachment(ctx, domain.GetPublishedAttachmentParams{
		ImageName:    "published-artifacts",
		Version:      "v1.0.0",
		ArtifactID:   publishedArtifact.ID,
		AttachmentID: foreignAttachment.ID,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCatalogAddsRawGZArtifact(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()

	createImage(t, ctx, catalogStore, "raw-gz-artifact")
	createVersion(t, ctx, catalogStore, "raw-gz-artifact", "v1.0.0")
	primaryDigest := catalogDigest("9")
	insertTrustedBlob(t, store, primaryDigest, 1024)

	artifact, err := catalogStore.AddArtifact(ctx, domain.AddArtifactParams{
		ImageName:            "raw-gz-artifact",
		Version:              "v1.0.0",
		OperatingSystem:      "incusos",
		Architecture:         "x86_64",
		Format:               domain.ArtifactFormatRawGZ,
		PrimaryBlobDigest:    primaryDigest,
		PrimaryBlobSizeBytes: 1024,
		PrimaryMediaType:     "application/gzip",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ArtifactFormatRawGZ, artifact.Format)
	assert.Equal(t, "application/gzip", artifact.PrimaryMediaType)

	manifest, err := catalogStore.GetVersionManifest(ctx, domain.GetVersionManifestParams{
		ImageName: "raw-gz-artifact",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 1)
	assert.Equal(t, domain.ArtifactFormatRawGZ, manifest.Artifacts[0].Artifact.Format)
	assert.Equal(t, primaryDigest, manifest.Artifacts[0].Artifact.PrimaryBlobDigest)
}

func startCatalogIntegrationStore(t *testing.T) *Store {
	t.Helper()

	ctx := t.Context()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("imgsrv"),
		tcpostgres.WithUsername("imgsrv"),
		tcpostgres.WithPassword("imgsrv"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return openIntegrationStore(t, ctx, databaseURL)
}

func insertDraftCatalogVersion(t *testing.T, store *Store, imageID uuid.UUID, version string, createdAt time.Time) {
	t.Helper()

	_, err := store.pool.Exec(
		t.Context(),
		`INSERT INTO image_versions (id, image_id, version, state, created_at, updated_at)
		VALUES ($1, $2, $3, 'draft', $4, $4)`,
		uuid.New(),
		imageID,
		version,
		createdAt,
	)
	require.NoError(t, err)
}

func insertPublishedCatalogVersion(t *testing.T, store *Store, imageID uuid.UUID, version string, createdAt time.Time) {
	t.Helper()

	ctx := t.Context()
	versionID := uuid.New()
	digest := publishedCatalogDigest(imageID, version)

	_, err := store.pool.Exec(
		ctx,
		`INSERT INTO image_versions (id, image_id, version, state, created_at, updated_at)
		VALUES ($1, $2, $3, 'draft', $4, $4)`,
		versionID,
		imageID,
		version,
		createdAt,
	)
	require.NoError(t, err)

	insertTrustedBlob(t, store, digest, 4096)
	_, err = store.pool.Exec(
		ctx,
		`INSERT INTO release_artifacts (
			id,
			version_id,
			operating_system,
			architecture,
			format,
			primary_blob_digest,
			primary_blob_size_bytes,
			primary_media_type,
			created_at,
			updated_at
		)
		VALUES ($1, $2, 'linux', 'amd64', 'raw', $3, 4096, 'application/octet-stream', $4, $4)`,
		uuid.New(),
		versionID,
		digest,
		createdAt,
	)
	require.NoError(t, err)

	_, err = store.pool.Exec(
		ctx,
		`UPDATE image_versions
		SET state = 'published',
			published_at = $2,
			updated_at = $2
		WHERE id = $1`,
		versionID,
		createdAt,
	)
	require.NoError(t, err)
}

func insertTrustedBlob(t *testing.T, store *Store, digest domain.Digest, sizeBytes int64) {
	t.Helper()

	uploadDigest := uploads.Digest(digest.String())
	_, err := store.pool.Exec(
		t.Context(),
		`INSERT INTO cas_blobs (digest, size_bytes, storage_key, verified_at)
		VALUES ($1, $2, $3, now())`,
		uploadDigest,
		sizeBytes,
		cas.StorageKey(uploadDigest),
	)
	require.NoError(t, err)
}

func createImage(t *testing.T, ctx context.Context, store domain.Store, name string) domain.Image {
	t.Helper()

	image, err := store.CreateImage(ctx, domain.CreateImageParams{Name: name})
	require.NoError(t, err)

	return image
}

func createVersion(
	t *testing.T,
	ctx context.Context,
	store domain.Store,
	imageName string,
	version string,
) domain.Version {
	t.Helper()

	imageVersion, err := store.CreateDraftVersion(ctx, domain.CreateDraftVersionParams{
		ImageName: imageName,
		Version:   version,
	})
	require.NoError(t, err)

	return imageVersion
}

func createArtifact(
	t *testing.T,
	ctx context.Context,
	store domain.Store,
	imageName string,
	version string,
	digest domain.Digest,
) domain.Artifact {
	t.Helper()

	artifact, err := store.AddArtifact(ctx, domain.AddArtifactParams{
		ImageName:            imageName,
		Version:              version,
		OperatingSystem:      "linux",
		Architecture:         "amd64",
		Format:               domain.ArtifactFormatRaw,
		PrimaryBlobDigest:    digest,
		PrimaryBlobSizeBytes: 4096,
		PrimaryMediaType:     "application/octet-stream",
	})
	require.NoError(t, err)

	return artifact
}

func createVariantArtifact(
	t *testing.T,
	ctx context.Context,
	store domain.Store,
	imageName string,
	version string,
	digest domain.Digest,
	architecture string,
) domain.Artifact {
	t.Helper()

	artifact, err := store.AddArtifact(ctx, domain.AddArtifactParams{
		ImageName:            imageName,
		Version:              version,
		OperatingSystem:      "linux",
		Architecture:         architecture,
		Format:               domain.ArtifactFormatRaw,
		PrimaryBlobDigest:    digest,
		PrimaryBlobSizeBytes: 2048,
		PrimaryMediaType:     "application/octet-stream",
	})
	require.NoError(t, err)

	return artifact
}

func createAttachment(
	t *testing.T,
	ctx context.Context,
	store domain.Store,
	imageName string,
	version string,
	artifactID uuid.UUID,
	name string,
	digest domain.Digest,
) domain.Attachment {
	t.Helper()

	attachment, err := store.AddAttachment(ctx, domain.AddAttachmentParams{
		ImageName:     imageName,
		Version:       version,
		ArtifactID:    artifactID,
		Name:          name,
		MediaType:     "text/plain",
		BlobDigest:    digest,
		BlobSizeBytes: 256,
	})
	require.NoError(t, err)

	return attachment
}

func assertNoAttachments(t *testing.T, store *Store, artifactID uuid.UUID) {
	t.Helper()

	var count int
	err := store.pool.QueryRow(
		t.Context(),
		`SELECT count(*) FROM artifact_attachments WHERE artifact_id = $1`,
		artifactID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func imageNames(images []domain.Image) []string {
	names := make([]string, 0, len(images))
	for _, image := range images {
		names = append(names, image.Name)
	}

	return names
}

func versionNames(versions []domain.Version) []string {
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		names = append(names, version.Version)
	}

	return names
}

func artifactIDs(artifacts []domain.Artifact) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(artifacts))
	for _, artifact := range artifacts {
		ids = append(ids, artifact.ID)
	}

	return ids
}

func catalogDigest(hexChar string) domain.Digest {
	return domain.Digest("sha256:" + strings.Repeat(hexChar, 64))
}

func publishedCatalogDigest(imageID uuid.UUID, version string) domain.Digest {
	sum := sha256.Sum256([]byte(imageID.String() + ":" + version))
	return domain.Digest("sha256:" + hex.EncodeToString(sum[:]))
}
