package incus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	simplestreams "github.com/imgoci/go-simplestreams"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	safelog "github.com/meigma/imgsrv/internal/logging"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

// ManifestCatalog reads frozen version manifests for publish-time indexing.
type ManifestCatalog interface {
	// GetVersionManifest returns an exact draft, publishing, or published image version manifest.
	GetVersionManifest(context.Context, catalog.GetVersionManifestParams) (catalog.Manifest, error)
}

// BlobReader opens trusted CAS blobs for publish-time checksum work.
type BlobReader interface {
	// OpenBlob opens a trusted CAS blob, optionally constrained to one byte range.
	OpenBlob(context.Context, cas.OpenBlobParams) (objectstore.ObjectReader, error)
}

// IndexerConfig configures publish-time Incus indexing.
type IndexerConfig struct {
	// Catalog reads frozen version manifests.
	Catalog ManifestCatalog

	// Blobs opens trusted CAS blob bytes.
	Blobs BlobReader

	// Logger receives Incus indexing logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Indexer computes Incus projection rows for frozen versions.
type Indexer struct {
	catalog ManifestCatalog
	blobs   BlobReader
	logger  *slog.Logger
}

// NewIndexer constructs an Incus publish-time indexer.
func NewIndexer(config IndexerConfig) *Indexer {
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	return &Indexer{
		catalog: config.Catalog,
		blobs:   config.Blobs,
		logger:  logger,
	}
}

// IndexVersion computes deterministic Incus projection rows for one frozen version.
func (indexer *Indexer) IndexVersion(ctx context.Context, params IndexVersionParams) ([]ProjectionRow, error) {
	catalogReader, blobs, err := indexer.dependencies()
	if err != nil {
		return nil, err
	}
	if validationErr := catalog.ValidateImageName(params.ImageName); validationErr != nil {
		return nil, validationErr
	}
	if validationErr := catalog.ValidateVersion(params.Version); validationErr != nil {
		return nil, validationErr
	}
	if params.VersionID == uuid.Nil {
		return nil, fmt.Errorf("%w: version id is required", catalog.ErrInvalid)
	}
	indexer.logger.InfoContext(
		ctx,
		"incus version indexing started",
		"operation",
		"incus.index_version",
		"version_id",
		params.VersionID.String(),
		"image_name",
		params.ImageName,
		"version",
		params.Version,
	)

	manifest, err := catalogReader.GetVersionManifest(ctx, catalog.GetVersionManifestParams{
		ImageName: params.ImageName,
		Version:   params.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("incus materialization: load manifest for image %q version %q: %w",
			params.ImageName,
			params.Version,
			err,
		)
	}

	state := indexState{
		blobs:          blobs,
		combinedSHA256: map[combinedKey]string{},
	}
	rows := []ProjectionRow{}
	skippedFormat := 0
	skippedMetadata := 0
	for _, manifestArtifact := range manifest.Artifacts {
		artifact := manifestArtifact.Artifact
		if artifact.Format != catalog.ArtifactFormatQCOW2 {
			skippedFormat++
			continue
		}
		metadataAttachment, ok := findMetadataAttachment(manifestArtifact.Attachments)
		if !ok {
			skippedMetadata++
			continue
		}

		row, err := state.rowForArtifact(ctx, manifest, artifact, metadataAttachment)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	indexer.logger.InfoContext(
		ctx,
		"incus version indexing completed",
		"operation",
		"incus.index_version",
		"version_id",
		params.VersionID.String(),
		"image_name",
		params.ImageName,
		"version",
		params.Version,
		"artifact_count",
		len(manifest.Artifacts),
		"projection_rows",
		len(rows),
		"skipped_format_count",
		skippedFormat,
		"skipped_missing_metadata_count",
		skippedMetadata,
	)

	return rows, nil
}

// IndexVersionParams identifies the version to index.
type IndexVersionParams struct {
	// VersionID identifies the frozen image version.
	VersionID uuid.UUID

	// ImageName is the image namespace.
	ImageName string

	// Version is the operator-defined version string.
	Version string
}

type indexState struct {
	blobs          BlobReader
	combinedSHA256 map[combinedKey]string
}

type combinedKey struct {
	metadata catalog.Digest
	disk     catalog.Digest
}

func (state *indexState) rowForArtifact(
	ctx context.Context,
	manifest catalog.Manifest,
	artifact catalog.Artifact,
	metadata catalog.Attachment,
) (ProjectionRow, error) {
	metadataPath, err := attachmentDownloadPath(manifest.Image.Name, manifest.Version.Version, artifact.ID, metadata.ID)
	if err != nil {
		return ProjectionRow{}, err
	}
	diskPath, err := artifactDownloadPath(manifest.Image.Name, manifest.Version.Version, artifact.ID)
	if err != nil {
		return ProjectionRow{}, err
	}

	metadataSHA256, err := digestSHA256Hex(metadata.BlobDigest)
	if err != nil {
		return ProjectionRow{}, err
	}
	diskSHA256, err := digestSHA256Hex(artifact.PrimaryBlobDigest)
	if err != nil {
		return ProjectionRow{}, err
	}
	combinedSHA256, err := state.combinedDiskSHA256(ctx, metadata.BlobDigest, artifact.PrimaryBlobDigest)
	if err != nil {
		return ProjectionRow{}, err
	}

	return ProjectionRow{
		VersionID:                manifest.Version.ID,
		ArtifactID:               artifact.ID,
		MetadataAttachmentID:     metadata.ID,
		ImageName:                manifest.Image.Name,
		ImageDisplayName:         manifest.Image.DisplayName,
		Version:                  manifest.Version.Version,
		VersionCreatedAt:         manifest.Version.CreatedAt,
		PublishedAt:              manifest.Version.PublishedAt,
		OperatingSystem:          artifact.OperatingSystem,
		Architecture:             artifact.Architecture,
		Variant:                  catalog.NormalizeArtifactVariant(artifact.Variant),
		MetadataPath:             metadataPath.String(),
		DiskPath:                 diskPath.String(),
		MetadataSHA256:           metadataSHA256,
		MetadataSizeBytes:        metadata.BlobSizeBytes,
		DiskSHA256:               diskSHA256,
		DiskSizeBytes:            artifact.PrimaryBlobSizeBytes,
		CombinedDiskKVMImgSHA256: combinedSHA256,
	}, nil
}

func (state *indexState) combinedDiskSHA256(
	ctx context.Context,
	metadataDigest catalog.Digest,
	diskDigest catalog.Digest,
) (string, error) {
	key := combinedKey{metadata: metadataDigest, disk: diskDigest}
	if value, ok := state.combinedSHA256[key]; ok {
		return value, nil
	}

	metadataReader, err := state.openBlob(ctx, metadataDigest)
	if err != nil {
		return "", err
	}
	defer func() { _ = metadataReader.Body.Close() }()

	diskReader, err := state.openBlob(ctx, diskDigest)
	if err != nil {
		return "", err
	}
	defer func() { _ = diskReader.Body.Close() }()

	value, err := simplestreams.SHA256Concat(metadataReader.Body, diskReader.Body)
	if err != nil {
		return "", fmt.Errorf("incus materialization: compute combined disk checksum: %w", err)
	}
	state.combinedSHA256[key] = value

	return value, nil
}

func (state *indexState) openBlob(
	ctx context.Context,
	digest catalog.Digest,
) (objectstore.ObjectReader, error) {
	reader, err := state.blobs.OpenBlob(ctx, cas.OpenBlobParams{
		Digest: uploads.Digest(digest.String()),
	})
	if err != nil {
		return objectstore.ObjectReader{}, fmt.Errorf("incus materialization: open blob %s: %w", digest, err)
	}

	return reader, nil
}

func findMetadataAttachment(attachments []catalog.Attachment) (catalog.Attachment, bool) {
	for _, attachment := range attachments {
		if attachment.Name == metadataItemName {
			return attachment, true
		}
	}

	return catalog.Attachment{}, false
}

func digestSHA256Hex(digest catalog.Digest) (string, error) {
	value := digest.String()
	hash, ok := strings.CutPrefix(value, "sha256:")
	if !ok {
		return "", fmt.Errorf("incus materialization: digest %q is not sha256", value)
	}
	if _, err := hex.DecodeString(hash); err != nil || len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("incus materialization: digest %q is not valid lowercase hex", value)
	}

	return hash, nil
}

func (indexer *Indexer) dependencies() (ManifestCatalog, BlobReader, error) {
	if indexer == nil || indexer.catalog == nil || indexer.blobs == nil {
		return nil, nil, errors.New("incus indexer dependencies are not configured")
	}

	return indexer.catalog, indexer.blobs, nil
}
