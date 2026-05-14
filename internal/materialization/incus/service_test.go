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

func TestBuildProductFileReturnsEmptyIncusProductFileForEmptyProjection(t *testing.T) {
	service := NewService(Config{
		Catalog: &fakeCatalog{},
		Store:   fakeProjectionStore{},
	})

	productFile, err := service.BuildProductFile(context.Background())

	require.NoError(t, err)
	assert.Equal(t, incusschema.ContentIDImages, productFile.ContentID)
	assert.Equal(t, incusschema.DataTypeImageDownloads, productFile.DataType)
	assert.Empty(t, productFile.Products)
	require.NoError(t, incusschema.ValidateRuntimeProductFile(productFile))
}

func TestIndexListsProjectedProductsWithoutOpeningBlobs(t *testing.T) {
	publishedAt := time.Date(2026, 5, 11, 5, 24, 0, 0, time.UTC)
	service := NewService(Config{
		Catalog: &fakeCatalog{},
		Store: fakeProjectionStore{rows: []ProjectionRow{{
			VersionID:        uuid.New(),
			ArtifactID:       uuid.New(),
			ImageName:        "image",
			Version:          "v1.0.0",
			VersionCreatedAt: publishedAt.Add(-time.Hour),
			PublishedAt:      &publishedAt,
			Architecture:     "x86_64",
		}}},
	})

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
}

func TestBuildProductFileProjectsPersistedRowWithLiveAliases(t *testing.T) {
	publishedAt := time.Date(2026, 5, 11, 5, 24, 0, 0, time.UTC)
	displayName := "Debian 12"
	artifactID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	attachmentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	imageID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	service := NewService(Config{
		Catalog: &fakeCatalog{
			aliases: map[string][]catalog.Alias{"debian": {
				{ImageID: imageID, Alias: "latest", VersionID: versionID, Version: "bookworm"},
				{ImageID: imageID, Alias: "old", VersionID: uuid.New(), Version: "bullseye"},
			}},
		},
		Store: fakeProjectionStore{rows: []ProjectionRow{
			{
				VersionID:                versionID,
				ArtifactID:               artifactID,
				MetadataAttachmentID:     attachmentID,
				ImageName:                "debian",
				ImageDisplayName:         &displayName,
				Version:                  "bookworm",
				VersionCreatedAt:         publishedAt.Add(-time.Hour),
				PublishedAt:              &publishedAt,
				OperatingSystem:          "linux",
				Architecture:             "x86_64",
				Variant:                  "secureboot",
				MetadataPath:             "v1/images/debian/versions/bookworm/artifacts/" + artifactID.String() + "/attachments/" + attachmentID.String() + "/download",
				DiskPath:                 "v1/images/debian/versions/bookworm/artifacts/" + artifactID.String() + "/download",
				MetadataSHA256:           digestHex(testCatalogDigest([]byte("metadata"))),
				MetadataSizeBytes:        int64(len("metadata")),
				DiskSHA256:               digestHex(testCatalogDigest([]byte("disk"))),
				DiskSizeBytes:            int64(len("disk")),
				CombinedDiskKVMImgSHA256: testCombinedSHA256([]byte("metadata"), []byte("disk")),
			},
		}},
	})

	productFile, err := service.BuildProductFile(context.Background())

	require.NoError(t, err)
	require.NoError(t, incusschema.ValidateRuntimeProductFile(productFile))
	product := productFile.Products["debian:bookworm:amd64:secureboot"]
	require.NotNil(t, product)
	assertMetadataValue(t, product, "aliases", "debian/bookworm,debian/latest")
	assertMetadataValue(t, product, "arch", "amd64")
	assertMetadataValue(t, product, "os", "linux")
	assertMetadataValue(t, product, "release", "bookworm")
	assertMetadataValue(t, product, "release_title", "Debian 12")
	assertMetadataValue(t, product, "variant", "secureboot")

	streamVersion := product.Versions["20260511_05:24"]
	require.NotNil(t, streamVersion)
	metadataItem := streamVersion.Items[metadataItemName]
	require.NotNil(t, metadataItem)
	assert.Equal(t, metadataFileType, metadataItem.FileType)
	assert.Equal(t, int64(len("metadata")), *metadataItem.Size)
	assertMetadataValue(
		t,
		metadataItem,
		"combined_disk-kvm-img_sha256",
		testCombinedSHA256([]byte("metadata"), []byte("disk")),
	)

	diskItem := streamVersion.Items[diskItemName]
	require.NotNil(t, diskItem)
	assert.Equal(t, diskFileType, diskItem.FileType)
	assert.Equal(t, int64(len("disk")), *diskItem.Size)
}

func TestIndexerProjectsEligibleQCOW2Artifact(t *testing.T) {
	ctx := context.Background()
	diskPayload := []byte("qcow2 disk bytes")
	metadataPayload := []byte("incus metadata bytes")
	diskDigest := testCatalogDigest(diskPayload)
	metadataDigest := testCatalogDigest(metadataPayload)
	image := catalog.Image{ID: uuid.New(), Name: "debian"}
	version := catalog.Version{
		ID:        uuid.New(),
		ImageID:   image.ID,
		Version:   "bookworm",
		State:     catalog.VersionStatePublishing,
		CreatedAt: time.Date(2026, 5, 11, 4, 24, 0, 0, time.UTC),
	}
	artifactID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	attachmentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	indexer := NewIndexer(IndexerConfig{
		Catalog: &fakeCatalog{
			manifests: map[string]map[string]catalog.Manifest{
				image.Name: {
					version.Version: manifestForArtifact(
						image,
						version,
						artifactID,
						attachmentID,
						catalog.ArtifactFormatQCOW2,
						"x86_64",
						diskDigest,
						metadataDigest,
					),
				},
			},
		},
		Blobs: newFakeBlobs(map[uploads.Digest][]byte{
			uploads.Digest(diskDigest.String()):     diskPayload,
			uploads.Digest(metadataDigest.String()): metadataPayload,
		}),
	})

	rows, err := indexer.IndexVersion(ctx, IndexVersionParams{
		VersionID: version.ID,
		ImageName: image.Name,
		Version:   version.Version,
	})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, version.ID, row.VersionID)
	assert.Equal(t, artifactID, row.ArtifactID)
	assert.Equal(t, attachmentID, row.MetadataAttachmentID)
	assert.Equal(t, "debian", row.ImageName)
	assert.Equal(t, "bookworm", row.Version)
	assert.Equal(t, "linux", row.OperatingSystem)
	assert.Equal(t, "x86_64", row.Architecture)
	assert.Equal(t, "secureboot", row.Variant)
	assert.Equal(t, digestHex(metadataDigest), row.MetadataSHA256)
	assert.Equal(t, digestHex(diskDigest), row.DiskSHA256)
	assert.Equal(t, testCombinedSHA256(metadataPayload, diskPayload), row.CombinedDiskKVMImgSHA256)
	assert.Equal(
		t,
		"v1/images/debian/versions/bookworm/artifacts/11111111-1111-1111-1111-111111111111/attachments/22222222-2222-2222-2222-222222222222/download",
		row.MetadataPath,
	)
}

func TestIndexerSkipsIneligibleArtifacts(t *testing.T) {
	tests := []struct {
		name           string
		format         catalog.ArtifactFormat
		attachmentName string
	}{
		{name: "non qcow2 artifact", format: catalog.ArtifactFormatRawGZ, attachmentName: metadataItemName},
		{
			name:           "qcow2 without incus metadata attachment",
			format:         catalog.ArtifactFormatQCOW2,
			attachmentName: "other.tar.xz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := catalog.Image{ID: uuid.New(), Name: "image"}
			version := catalog.Version{ID: uuid.New(), ImageID: image.ID, Version: "v1.0.0"}
			diskDigest := testCatalogDigest([]byte("disk"))
			metadataDigest := testCatalogDigest([]byte("metadata"))
			manifest := manifestForArtifact(
				image,
				version,
				uuid.New(),
				uuid.New(),
				tt.format,
				"x86_64",
				diskDigest,
				metadataDigest,
			)
			manifest.Artifacts[0].Attachments[0].Name = tt.attachmentName
			indexer := NewIndexer(IndexerConfig{
				Catalog: &fakeCatalog{
					manifests: map[string]map[string]catalog.Manifest{image.Name: {version.Version: manifest}},
				},
				Blobs: newFakeBlobs(map[uploads.Digest][]byte{}),
			})

			rows, err := indexer.IndexVersion(context.Background(), IndexVersionParams{
				VersionID: version.ID,
				ImageName: image.Name,
				Version:   version.Version,
			})

			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

func TestIndexerDeduplicatesCombinedHashReadsPerVersion(t *testing.T) {
	diskPayload := []byte("shared disk")
	metadataPayload := []byte("shared metadata")
	diskDigest := testCatalogDigest(diskPayload)
	metadataDigest := testCatalogDigest(metadataPayload)
	blobs := newFakeBlobs(map[uploads.Digest][]byte{
		uploads.Digest(diskDigest.String()):     diskPayload,
		uploads.Digest(metadataDigest.String()): metadataPayload,
	})
	image := catalog.Image{ID: uuid.New(), Name: "image"}
	version := catalog.Version{ID: uuid.New(), ImageID: image.ID, Version: "v1.0.0"}
	first := manifestForArtifact(
		image,
		version,
		uuid.New(),
		uuid.New(),
		catalog.ArtifactFormatQCOW2,
		"x86_64",
		diskDigest,
		metadataDigest,
	)
	secondArtifactID := uuid.New()
	first.Artifacts = append(first.Artifacts, manifestForArtifact(
		image,
		version,
		secondArtifactID,
		uuid.New(),
		catalog.ArtifactFormatQCOW2,
		"aarch64",
		diskDigest,
		metadataDigest,
	).Artifacts...)
	indexer := NewIndexer(IndexerConfig{
		Catalog: &fakeCatalog{
			manifests: map[string]map[string]catalog.Manifest{image.Name: {version.Version: first}},
		},
		Blobs: blobs,
	})

	rows, err := indexer.IndexVersion(context.Background(), IndexVersionParams{
		VersionID: version.ID,
		ImageName: image.Name,
		Version:   version.Version,
	})

	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 1, blobs.opens[uploads.Digest(metadataDigest.String())])
	assert.Equal(t, 1, blobs.opens[uploads.Digest(diskDigest.String())])
}

func manifestForArtifact(
	image catalog.Image,
	version catalog.Version,
	artifactID uuid.UUID,
	attachmentID uuid.UUID,
	format catalog.ArtifactFormat,
	architecture string,
	diskDigest catalog.Digest,
	metadataDigest catalog.Digest,
) catalog.Manifest {
	return catalog.Manifest{
		Image:   image,
		Version: version,
		Artifacts: []catalog.ManifestArtifact{{
			Artifact: catalog.Artifact{
				ID:                   artifactID,
				VersionID:            version.ID,
				OperatingSystem:      "linux",
				Architecture:         architecture,
				Variant:              "secureboot",
				Format:               format,
				PrimaryBlobDigest:    diskDigest,
				PrimaryBlobSizeBytes: int64(len("disk")),
				PrimaryMediaType:     "application/x-qcow2",
			},
			Attachments: []catalog.Attachment{{
				ID:            attachmentID,
				ArtifactID:    artifactID,
				Name:          metadataItemName,
				BlobDigest:    metadataDigest,
				BlobSizeBytes: int64(len("metadata")),
			}},
		}},
	}
}

type fakeProjectionStore struct {
	rows []ProjectionRow
}

func (fake fakeProjectionStore) ListProjectionRows(context.Context) ([]ProjectionRow, error) {
	return fake.rows, nil
}

type fakeCatalog struct {
	aliases   map[string][]catalog.Alias
	manifests map[string]map[string]catalog.Manifest
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
