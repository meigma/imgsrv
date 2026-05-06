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

func TestServiceDelegatesCatalogBrowseOperations(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := catalog.NewService(catalog.ServiceConfig{Store: store})
	wantImage := catalog.Image{
		ID:   uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		Name: "debian",
	}
	wantVersion := catalog.Version{
		ID:      uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff"),
		ImageID: wantImage.ID,
		Version: "v1.0.0",
		State:   catalog.VersionStateDraft,
	}

	store.EXPECT().
		ListImages(context.Background(), catalog.ListImagesParams{}).
		Return([]catalog.Image{wantImage}, nil)
	store.EXPECT().
		GetImage(context.Background(), catalog.GetImageParams{Name: "debian"}).
		Return(wantImage, nil)
	store.EXPECT().
		ListVersions(context.Background(), catalog.ListVersionsParams{ImageName: "debian"}).
		Return([]catalog.Version{wantVersion}, nil)

	images, err := service.ListImages(context.Background(), catalog.ListImagesParams{})
	require.NoError(t, err)
	assert.Equal(t, []catalog.Image{wantImage}, images)

	image, err := service.GetImage(context.Background(), catalog.GetImageParams{Name: "debian"})
	require.NoError(t, err)
	assert.Equal(t, wantImage, image)

	versions, err := service.ListVersions(context.Background(), catalog.ListVersionsParams{ImageName: "debian"})
	require.NoError(t, err)
	assert.Equal(t, []catalog.Version{wantVersion}, versions)
}

func TestServiceDelegatesAliasOperations(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := catalog.NewService(catalog.ServiceConfig{Store: store})
	wantAlias := catalog.Alias{
		ID:        uuid.MustParse("eeeeeeee-ffff-0000-1111-222222222222"),
		ImageID:   uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		Alias:     "latest",
		VersionID: uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff"),
		Version:   "v1.0.0",
	}

	store.EXPECT().
		PutAlias(context.Background(), catalog.PutAliasParams{
			ImageName: "debian",
			Alias:     "latest",
			Version:   "v1.0.0",
		}).
		Return(wantAlias, nil)
	store.EXPECT().
		ListAliases(context.Background(), catalog.ListAliasesParams{ImageName: "debian"}).
		Return([]catalog.Alias{wantAlias}, nil)
	store.EXPECT().
		GetAlias(context.Background(), catalog.GetAliasParams{
			ImageName: "debian",
			Alias:     "latest",
		}).
		Return(wantAlias, nil)
	store.EXPECT().
		DeleteAlias(context.Background(), catalog.DeleteAliasParams{
			ImageName: "debian",
			Alias:     "latest",
		}).
		Return(nil)

	put, err := service.PutAlias(context.Background(), catalog.PutAliasParams{
		ImageName: "debian",
		Alias:     "latest",
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, wantAlias, put)

	list, err := service.ListAliases(context.Background(), catalog.ListAliasesParams{ImageName: "debian"})
	require.NoError(t, err)
	assert.Equal(t, []catalog.Alias{wantAlias}, list)

	get, err := service.GetAlias(context.Background(), catalog.GetAliasParams{
		ImageName: "debian",
		Alias:     "latest",
	})
	require.NoError(t, err)
	assert.Equal(t, wantAlias, get)

	err = service.DeleteAlias(context.Background(), catalog.DeleteAliasParams{
		ImageName: "debian",
		Alias:     "latest",
	})
	require.NoError(t, err)
}

func TestServiceDelegatesDraftDeletes(t *testing.T) {
	store := mocks.NewMockStore(t)
	service := catalog.NewService(catalog.ServiceConfig{Store: store})
	artifactID := uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000")
	attachmentID := uuid.MustParse("dddddddd-eeee-ffff-0000-111111111111")

	store.EXPECT().
		DeleteArtifact(context.Background(), catalog.DeleteArtifactParams{
			ImageName:  "debian",
			Version:    "v1.0.0",
			ArtifactID: artifactID,
		}).
		Return(nil)
	store.EXPECT().
		DeleteAttachment(context.Background(), catalog.DeleteAttachmentParams{
			ImageName:    "debian",
			Version:      "v1.0.0",
			ArtifactID:   artifactID,
			AttachmentID: attachmentID,
		}).
		Return(nil)

	err := service.DeleteArtifact(context.Background(), catalog.DeleteArtifactParams{
		ImageName:  "debian",
		Version:    "v1.0.0",
		ArtifactID: artifactID,
	})
	require.NoError(t, err)

	err = service.DeleteAttachment(context.Background(), catalog.DeleteAttachmentParams{
		ImageName:    "debian",
		Version:      "v1.0.0",
		ArtifactID:   artifactID,
		AttachmentID: attachmentID,
	})
	require.NoError(t, err)
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
