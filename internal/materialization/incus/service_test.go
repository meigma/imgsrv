package incus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestBuildProductFileReturnsEmptyIncusProductFileForEmptyCatalog(t *testing.T) {
	service := NewService(Config{
		Catalog: &fakeCatalog{},
		Blobs:   newFakeBlobs(nil),
	})

	productFile, err := service.BuildProductFile(context.Background())

	require.NoError(t, err)
	assert.Equal(t, incusschema.ContentIDImages, productFile.ContentID)
	assert.Equal(t, incusschema.DataTypeImageDownloads, productFile.DataType)
	assert.Empty(t, productFile.Products)
	require.NoError(t, incusschema.ValidateRuntimeProductFile(productFile))
}

func TestIndexListsProjectedProductsWithoutOpeningBlobs(t *testing.T) {
	metadataPayload := []byte("metadata")
	metadataDigest := testCatalogDigest(metadataPayload)
	service, blobs := serviceAndBlobsForSingleArtifact(t, catalog.ArtifactFormatQCOW2, "x86_64", []catalog.Attachment{{
		ID:            uuid.New(),
		ArtifactID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Name:          metadataItemName,
		BlobDigest:    metadataDigest,
		BlobSizeBytes: int64(len(metadataPayload)),
	}})

	body, err := service.Index(context.Background())

	require.NoError(t, err)
	var index struct {
		Index map[string]struct {
			DataType string   `json:"datatype"`
			Format   string   `json:"format"`
			Products []string `json:"products"`
		} `json:"index"`
	}
	require.NoError(t, json.Unmarshal(body, &index))
	entry, ok := index.Index[incusschema.ContentIDImages]
	require.True(t, ok)
	assert.Equal(t, simplestreams.ProductsFormat, entry.Format)
	assert.Equal(t, incusschema.DataTypeImageDownloads, entry.DataType)
	assert.Equal(t, []string{"image:v1.0.0:amd64:default"}, entry.Products)
	assert.Empty(t, blobs.opens)
}

func TestBuildProductFileProjectsEligibleQCOW2Artifact(t *testing.T) {
	ctx := context.Background()
	publishedAt := time.Date(2026, 5, 11, 5, 24, 0, 0, time.UTC)
	diskPayload := []byte("qcow2 disk bytes")
	metadataPayload := []byte("incus metadata bytes")
	diskDigest := testCatalogDigest(diskPayload)
	metadataDigest := testCatalogDigest(metadataPayload)
	displayName := "Debian 12"
	artifactID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	attachmentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	image := catalog.Image{
		ID:          uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		Name:        "debian",
		DisplayName: &displayName,
	}
	version := catalog.Version{
		ID:          uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		ImageID:     image.ID,
		Version:     "bookworm",
		State:       catalog.VersionStatePublished,
		PublishedAt: &publishedAt,
		CreatedAt:   publishedAt.Add(-time.Hour),
	}
	artifact := catalog.Artifact{
		ID:                   artifactID,
		VersionID:            version.ID,
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               catalog.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    diskDigest,
		PrimaryBlobSizeBytes: int64(len(diskPayload)),
		PrimaryMediaType:     "application/x-qcow2",
	}
	attachment := catalog.Attachment{
		ID:            attachmentID,
		ArtifactID:    artifactID,
		Name:          metadataItemName,
		MediaType:     "application/x-xz",
		BlobDigest:    metadataDigest,
		BlobSizeBytes: int64(len(metadataPayload)),
	}
	service := NewService(Config{
		Catalog: &fakeCatalog{
			images:   []catalog.Image{image},
			versions: map[string][]catalog.Version{image.Name: {version}},
			aliases: map[string][]catalog.Alias{image.Name: {
				{ImageID: image.ID, Alias: "latest", VersionID: version.ID, Version: version.Version},
				{ImageID: image.ID, Alias: "old", VersionID: uuid.New(), Version: "bullseye"},
			}},
			manifests: map[string]map[string]catalog.Manifest{
				image.Name: {
					version.Version: {
						Image:   image,
						Version: version,
						Artifacts: []catalog.ManifestArtifact{{
							Artifact:    artifact,
							Attachments: []catalog.Attachment{attachment},
						}},
					},
				},
			},
		},
		Blobs: newFakeBlobs(map[uploads.Digest][]byte{
			uploads.Digest(diskDigest.String()):     diskPayload,
			uploads.Digest(metadataDigest.String()): metadataPayload,
		}),
	})

	productFile, err := service.BuildProductFile(ctx)

	require.NoError(t, err)
	require.NoError(t, incusschema.ValidateRuntimeProductFile(productFile))
	product := productFile.Products["debian:bookworm:amd64:default"]
	require.NotNil(t, product)
	assertMetadataValue(t, product, "aliases", "debian/bookworm,debian/latest")
	assertMetadataValue(t, product, "arch", "amd64")
	assertMetadataValue(t, product, "os", "linux")
	assertMetadataValue(t, product, "release", "bookworm")
	assertMetadataValue(t, product, "release_title", "Debian 12")
	assertMetadataValue(t, product, "variant", "default")

	streamVersion := product.Versions["20260511_05:24"]
	require.NotNil(t, streamVersion)
	metadataItem := streamVersion.Items[metadataItemName]
	require.NotNil(t, metadataItem)
	assert.Equal(t, metadataFileType, metadataItem.FileType)
	assert.Equal(
		t,
		"v1/images/debian/versions/bookworm/artifacts/11111111-1111-1111-1111-111111111111/attachments/22222222-2222-2222-2222-222222222222/download",
		metadataItem.Path.String(),
	)
	assert.Equal(t, int64(len(metadataPayload)), *metadataItem.Size)
	assert.Equal(t, digestHex(metadataDigest), metadataItem.SHA256)
	assertMetadataValue(
		t,
		metadataItem,
		"combined_disk-kvm-img_sha256",
		testCombinedSHA256(metadataPayload, diskPayload),
	)

	diskItem := streamVersion.Items[diskItemName]
	require.NotNil(t, diskItem)
	assert.Equal(t, diskFileType, diskItem.FileType)
	assert.Equal(
		t,
		"v1/images/debian/versions/bookworm/artifacts/11111111-1111-1111-1111-111111111111/download",
		diskItem.Path.String(),
	)
	assert.Equal(t, int64(len(diskPayload)), *diskItem.Size)
	assert.Equal(t, digestHex(diskDigest), diskItem.SHA256)
}

func TestBuildProductFileSkipsIneligibleArtifacts(t *testing.T) {
	tests := []struct {
		name        string
		format      catalog.ArtifactFormat
		attachments []catalog.Attachment
	}{
		{
			name:   "non qcow2 artifact",
			format: catalog.ArtifactFormatRawGZ,
			attachments: []catalog.Attachment{{
				ID:            uuid.New(),
				ArtifactID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				Name:          metadataItemName,
				BlobDigest:    testCatalogDigest([]byte("metadata")),
				BlobSizeBytes: int64(len("metadata")),
			}},
		},
		{
			name:   "qcow2 without incus metadata attachment",
			format: catalog.ArtifactFormatQCOW2,
			attachments: []catalog.Attachment{{
				ID:            uuid.New(),
				ArtifactID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				Name:          "other-metadata.tar.xz",
				BlobDigest:    testCatalogDigest([]byte("metadata")),
				BlobSizeBytes: int64(len("metadata")),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := serviceForSingleArtifact(t, tt.format, "x86_64", tt.attachments)

			productFile, err := service.BuildProductFile(context.Background())

			require.NoError(t, err)
			assert.Empty(t, productFile.Products)
		})
	}
}

func TestBuildProductFileNormalizesArmArchitecture(t *testing.T) {
	metadataPayload := []byte("metadata")
	metadataDigest := testCatalogDigest(metadataPayload)
	service := serviceForSingleArtifact(t, catalog.ArtifactFormatQCOW2, "aarch64", []catalog.Attachment{{
		ID:            uuid.New(),
		ArtifactID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Name:          metadataItemName,
		BlobDigest:    metadataDigest,
		BlobSizeBytes: int64(len(metadataPayload)),
	}})

	productFile, err := service.BuildProductFile(context.Background())

	require.NoError(t, err)
	product := productFile.Products["image:v1.0.0:arm64:default"]
	require.NotNil(t, product)
	assertMetadataValue(t, product, "arch", "arm64")
}

func TestBuildProductFileDeduplicatesCombinedHashReadsPerRequest(t *testing.T) {
	publishedAt := time.Date(2026, 5, 11, 5, 24, 0, 0, time.UTC)
	diskPayload := []byte("shared disk")
	metadataPayload := []byte("shared metadata")
	diskDigest := testCatalogDigest(diskPayload)
	metadataDigest := testCatalogDigest(metadataPayload)
	blobs := newFakeBlobs(map[uploads.Digest][]byte{
		uploads.Digest(diskDigest.String()):     diskPayload,
		uploads.Digest(metadataDigest.String()): metadataPayload,
	})
	firstImage := catalog.Image{ID: uuid.New(), Name: "first"}
	secondImage := catalog.Image{ID: uuid.New(), Name: "second"}
	firstVersion := catalog.Version{
		ID:          uuid.New(),
		ImageID:     firstImage.ID,
		Version:     "v1.0.0",
		PublishedAt: &publishedAt,
	}
	secondVersion := catalog.Version{
		ID:          uuid.New(),
		ImageID:     secondImage.ID,
		Version:     "v1.0.0",
		PublishedAt: &publishedAt,
	}
	service := NewService(Config{
		Catalog: &fakeCatalog{
			images: []catalog.Image{firstImage, secondImage},
			versions: map[string][]catalog.Version{
				firstImage.Name:  {firstVersion},
				secondImage.Name: {secondVersion},
			},
			manifests: map[string]map[string]catalog.Manifest{
				firstImage.Name: {
					firstVersion.Version: manifestForDigestPair(firstImage, firstVersion, diskDigest, metadataDigest),
				},
				secondImage.Name: {
					secondVersion.Version: manifestForDigestPair(
						secondImage,
						secondVersion,
						diskDigest,
						metadataDigest,
					),
				},
			},
		},
		Blobs: blobs,
	})

	productFile, err := service.BuildProductFile(context.Background())

	require.NoError(t, err)
	require.Len(t, productFile.Products, 2)
	assert.Equal(t, 1, blobs.opens[uploads.Digest(metadataDigest.String())])
	assert.Equal(t, 1, blobs.opens[uploads.Digest(diskDigest.String())])
}

func serviceForSingleArtifact(
	t testing.TB,
	format catalog.ArtifactFormat,
	architecture string,
	attachments []catalog.Attachment,
) *Service {
	t.Helper()

	service, _ := serviceAndBlobsForSingleArtifact(t, format, architecture, attachments)

	return service
}

func serviceAndBlobsForSingleArtifact(
	t testing.TB,
	format catalog.ArtifactFormat,
	architecture string,
	attachments []catalog.Attachment,
) (*Service, *fakeBlobs) {
	t.Helper()

	publishedAt := time.Date(2026, 5, 11, 5, 24, 0, 0, time.UTC)
	diskPayload := []byte("disk")
	diskDigest := testCatalogDigest(diskPayload)
	image := catalog.Image{ID: uuid.New(), Name: "image"}
	version := catalog.Version{
		ID:          uuid.New(),
		ImageID:     image.ID,
		Version:     "v1.0.0",
		State:       catalog.VersionStatePublished,
		PublishedAt: &publishedAt,
	}
	blobs := map[uploads.Digest][]byte{uploads.Digest(diskDigest.String()): diskPayload}
	for _, attachment := range attachments {
		blobs[uploads.Digest(attachment.BlobDigest.String())] = []byte("metadata")
	}
	blobReader := newFakeBlobs(blobs)

	service := NewService(Config{
		Catalog: &fakeCatalog{
			images:   []catalog.Image{image},
			versions: map[string][]catalog.Version{image.Name: {version}},
			manifests: map[string]map[string]catalog.Manifest{
				image.Name: {
					version.Version: {
						Image:   image,
						Version: version,
						Artifacts: []catalog.ManifestArtifact{{
							Artifact: catalog.Artifact{
								ID:                   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
								VersionID:            version.ID,
								OperatingSystem:      "linux",
								Architecture:         architecture,
								Format:               format,
								PrimaryBlobDigest:    diskDigest,
								PrimaryBlobSizeBytes: int64(len(diskPayload)),
								PrimaryMediaType:     "application/x-qcow2",
							},
							Attachments: attachments,
						}},
					},
				},
			},
		},
		Blobs: blobReader,
	})

	return service, blobReader
}

func manifestForDigestPair(
	image catalog.Image,
	version catalog.Version,
	diskDigest catalog.Digest,
	metadataDigest catalog.Digest,
) catalog.Manifest {
	artifactID := uuid.New()

	return catalog.Manifest{
		Image:   image,
		Version: version,
		Artifacts: []catalog.ManifestArtifact{{
			Artifact: catalog.Artifact{
				ID:                   artifactID,
				VersionID:            version.ID,
				OperatingSystem:      "linux",
				Architecture:         "x86_64",
				Format:               catalog.ArtifactFormatQCOW2,
				PrimaryBlobDigest:    diskDigest,
				PrimaryBlobSizeBytes: int64(len("shared disk")),
				PrimaryMediaType:     "application/x-qcow2",
			},
			Attachments: []catalog.Attachment{{
				ID:            uuid.New(),
				ArtifactID:    artifactID,
				Name:          metadataItemName,
				BlobDigest:    metadataDigest,
				BlobSizeBytes: int64(len("shared metadata")),
			}},
		}},
	}
}

type fakeCatalog struct {
	images    []catalog.Image
	versions  map[string][]catalog.Version
	aliases   map[string][]catalog.Alias
	manifests map[string]map[string]catalog.Manifest
}

func (fake *fakeCatalog) ListImages(context.Context, catalog.ListImagesParams) ([]catalog.Image, error) {
	return fake.images, nil
}

func (fake *fakeCatalog) ListVersions(
	_ context.Context,
	params catalog.ListVersionsParams,
) ([]catalog.Version, error) {
	return fake.versions[params.ImageName], nil
}

func (fake *fakeCatalog) ListAliases(_ context.Context, params catalog.ListAliasesParams) ([]catalog.Alias, error) {
	return fake.aliases[params.ImageName], nil
}

func (fake *fakeCatalog) GetVersionManifest(
	_ context.Context,
	params catalog.GetVersionManifestParams,
) (catalog.Manifest, error) {
	manifest, ok := fake.manifests[params.ImageName][params.Version]
	if !ok {
		return catalog.Manifest{}, catalog.ErrNotFound
	}

	return manifest, nil
}

type fakeBlobs struct {
	payloads map[uploads.Digest][]byte
	opens    map[uploads.Digest]int
}

func newFakeBlobs(payloads map[uploads.Digest][]byte) *fakeBlobs {
	return &fakeBlobs{
		payloads: payloads,
		opens:    map[uploads.Digest]int{},
	}
}

func (fake *fakeBlobs) OpenBlob(
	_ context.Context,
	params cas.OpenBlobParams,
) (objectstore.ObjectReader, error) {
	payload, ok := fake.payloads[params.Digest]
	if !ok {
		return objectstore.ObjectReader{}, cas.ErrNotFound
	}
	fake.opens[params.Digest]++

	return objectstore.ObjectReader{
		Info: objectstore.ObjectInfo{
			Key:       cas.StorageKey(params.Digest),
			SizeBytes: int64(len(payload)),
		},
		Body: io.NopCloser(bytes.NewReader(payload)),
	}, nil
}

func assertMetadataValue(t testing.TB, holder interface {
	MetadataValue(string) (any, bool)
}, key string, want any) {
	t.Helper()

	got, ok := holder.MetadataValue(key)
	require.True(t, ok, "missing metadata %q", key)
	assert.Equal(t, want, got)
}

func testCatalogDigest(payload []byte) catalog.Digest {
	sum := sha256.Sum256(payload)
	return catalog.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func digestHex(digest catalog.Digest) string {
	value, _ := strings.CutPrefix(digest.String(), "sha256:")
	return value
}

func testCombinedSHA256(payloads ...[]byte) string {
	hasher := sha256.New()
	for _, payload := range payloads {
		_, _ = hasher.Write(payload)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}
