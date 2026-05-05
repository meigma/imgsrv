package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/catalog/mocks"
)

func TestServiceAddAttachmentPreservesArtifactOwnershipPath(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := catalog.NewService(catalog.ServiceConfig{Store: store})
	artifactID := uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
	wantAttachment := catalog.Attachment{
		ID:            uuid.MustParse("dddddddd-eeee-ffff-0000-111111111111"),
		ArtifactID:    artifactID,
		Name:          "rootfs.sha256",
		MediaType:     "text/plain",
		BlobDigest:    catalogDigestFixture(),
		BlobSizeBytes: 64,
	}

	store.EXPECT().
		AddAttachment(context.Background(), catalog.AddAttachmentParams{
			ImageName:     "debian",
			Version:       "v1.0.0",
			ArtifactID:    artifactID,
			Name:          "rootfs.sha256",
			MediaType:     "text/plain",
			BlobDigest:    catalogDigestFixture(),
			BlobSizeBytes: 64,
		}).
		Return(wantAttachment, nil)

	got, err := service.AddAttachment(context.Background(), catalog.AddAttachmentParams{
		ImageName:     "debian",
		Version:       "v1.0.0",
		ArtifactID:    artifactID,
		Name:          "rootfs.sha256",
		MediaType:     "text/plain",
		BlobDigest:    catalogDigestFixture(),
		BlobSizeBytes: 64,
	})

	require.NoError(t, err)
	assert.Equal(t, wantAttachment, got)
}

func TestServiceDelegatesManifestReads(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := catalog.NewService(catalog.ServiceConfig{Store: store})
	wantManifest := catalog.Manifest{
		Image: catalog.Image{
			ID:   uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
			Name: "debian",
		},
		Version: catalog.Version{
			ID:      uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff"),
			ImageID: uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
			Version: "v1.0.0",
			State:   catalog.VersionStatePublished,
		},
	}

	store.EXPECT().
		GetVersionManifest(context.Background(), catalog.GetVersionManifestParams{
			ImageName: "debian",
			Version:   "v1.0.0",
		}).
		Return(wantManifest, nil)
	store.EXPECT().
		ResolveManifest(context.Background(), catalog.ResolveManifestParams{
			ImageName: "debian",
			Version:   "latest",
		}).
		Return(wantManifest, nil)

	gotExact, err := service.GetVersionManifest(context.Background(), catalog.GetVersionManifestParams{
		ImageName: "debian",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, wantManifest, gotExact)

	gotAlias, err := service.ResolveManifest(context.Background(), catalog.ResolveManifestParams{
		ImageName: "debian",
		Version:   "latest",
	})
	require.NoError(t, err)
	assert.Equal(t, wantManifest, gotAlias)
}

func TestServiceRequiresStore(t *testing.T) {
	_, err := catalog.NewService(catalog.ServiceConfig{}).CreateImage(
		context.Background(),
		catalog.CreateImageParams{Name: "debian"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "catalog store is not configured")
}

func catalogDigestFixture() catalog.Digest {
	return catalog.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}
