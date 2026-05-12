// Package incus projects published imgsrv catalog state as an Incus Simple Streams mirror.
package incus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	// ImagesProductPath is the Simple Streams product document path served by imgsrv.
	ImagesProductPath simplestreams.RelativePath = "streams/v1/images.json"

	metadataItemName = "incus.tar.xz"
	metadataFileType = "incus.tar.xz"
	diskItemName     = "disk-kvm.img"
	diskFileType     = "disk-kvm.img"
	defaultVariant   = "default"
)

// Catalog reads the published catalog state needed to project an Incus stream.
type Catalog interface {
	// ListImages returns image namespaces with published versions.
	ListImages(context.Context, catalog.ListImagesParams) ([]catalog.Image, error)

	// ListVersions returns published versions for one image.
	ListVersions(context.Context, catalog.ListVersionsParams) ([]catalog.Version, error)

	// ListAliases returns aliases for one image.
	ListAliases(context.Context, catalog.ListAliasesParams) ([]catalog.Alias, error)

	// GetVersionManifest returns an exact draft or published version manifest.
	GetVersionManifest(context.Context, catalog.GetVersionManifestParams) (catalog.Manifest, error)
}

// BlobReader opens trusted CAS blobs for projection-time checksum work.
type BlobReader interface {
	// OpenBlob opens a trusted CAS blob, optionally constrained to one byte range.
	OpenBlob(context.Context, cas.OpenBlobParams) (objectstore.ObjectReader, error)
}

// Config configures the Incus Simple Streams projection service.
type Config struct {
	// Catalog reads published imgsrv catalog state.
	Catalog Catalog

	// Blobs opens trusted CAS blob bytes.
	Blobs BlobReader
}

// Service builds Incus-compatible Simple Streams metadata from imgsrv catalog state.
type Service struct {
	catalog Catalog
	blobs   BlobReader
}

// NewService constructs an Incus projection service.
func NewService(config Config) *Service {
	return &Service{
		catalog: config.Catalog,
		blobs:   config.Blobs,
	}
}

// Index renders the Simple Streams index document.
func (service *Service) Index(ctx context.Context) ([]byte, error) {
	catalogReader, _, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	products, err := projectedProductNames(ctx, catalogReader)
	if err != nil {
		return nil, err
	}

	index, err := simplestreams.BuildIndex([]simplestreams.BuildIndexEntry{{
		ContentID: incusschema.ContentIDImages,
		Path:      ImagesProductPath,
		Format:    simplestreams.ProductsFormat,
		DataType:  incusschema.DataTypeImageDownloads,
		Products:  products,
	}}, "")
	if err != nil {
		return nil, err
	}

	return simplestreams.MarshalJSONDocument(index)
}

// ProductFile renders the Incus image product document.
func (service *Service) ProductFile(ctx context.Context) ([]byte, error) {
	productFile, err := service.BuildProductFile(ctx)
	if err != nil {
		return nil, err
	}
	if err := incusschema.ValidateRuntimeProductFile(productFile); err != nil {
		return nil, err
	}

	return simplestreams.MarshalJSONDocument(productFile)
}

// BuildProductFile builds the Incus image product document as Simple Streams domain types.
func (service *Service) BuildProductFile(ctx context.Context) (*simplestreams.ProductFile, error) {
	catalogReader, blobs, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	projection := projectionState{
		blobs:          blobs,
		combinedSHA256: map[combinedKey]string{},
	}
	productFile := simplestreams.NewProductFile(incusschema.ContentIDImages)
	productFile.DataType = incusschema.DataTypeImageDownloads

	images, err := catalogReader.ListImages(ctx, catalog.ListImagesParams{})
	if err != nil {
		return nil, fmt.Errorf("incus materialization: list images: %w", err)
	}

	for _, image := range images {
		if err := service.projectImage(ctx, catalogReader, &projection, productFile, image); err != nil {
			return nil, err
		}
	}

	return productFile, nil
}

func projectedProductNames(ctx context.Context, catalogReader Catalog) ([]string, error) {
	images, err := catalogReader.ListImages(ctx, catalog.ListImagesParams{})
	if err != nil {
		return nil, fmt.Errorf("incus materialization: list images: %w", err)
	}

	names := []string{}
	seen := map[string]struct{}{}
	for _, image := range images {
		if err := appendImageProductNames(ctx, catalogReader, image, &names, seen); err != nil {
			return nil, err
		}
	}
	sort.Strings(names)

	return names, nil
}

func appendImageProductNames(
	ctx context.Context,
	catalogReader Catalog,
	image catalog.Image,
	names *[]string,
	seen map[string]struct{},
) error {
	versions, err := catalogReader.ListVersions(ctx, catalog.ListVersionsParams{ImageName: image.Name})
	if err != nil {
		return fmt.Errorf("incus materialization: list versions for image %q: %w", image.Name, err)
	}
	for _, version := range versions {
		manifest, err := catalogReader.GetVersionManifest(ctx, catalog.GetVersionManifestParams{
			ImageName: image.Name,
			Version:   version.Version,
		})
		if err != nil {
			return fmt.Errorf(
				"incus materialization: load manifest for image %q version %q: %w",
				image.Name,
				version.Version,
				err,
			)
		}
		if err := appendManifestProductNames(manifest, names, seen); err != nil {
			return err
		}
	}

	return nil
}

func appendManifestProductNames(
	manifest catalog.Manifest,
	names *[]string,
	seen map[string]struct{},
) error {
	for _, manifestArtifact := range manifest.Artifacts {
		artifact := manifestArtifact.Artifact
		if artifact.Format != catalog.ArtifactFormatQCOW2 {
			continue
		}
		if _, ok := findMetadataAttachment(manifestArtifact.Attachments); !ok {
			continue
		}

		name := productName(manifest.Image.Name, manifest.Version.Version, artifact.Architecture)
		if _, exists := seen[name]; exists {
			return fmt.Errorf("incus materialization: duplicate product %q", name)
		}
		seen[name] = struct{}{}
		*names = append(*names, name)
	}

	return nil
}

func (service *Service) projectImage(
	ctx context.Context,
	catalogReader Catalog,
	projection *projectionState,
	productFile *simplestreams.ProductFile,
	image catalog.Image,
) error {
	versions, err := catalogReader.ListVersions(ctx, catalog.ListVersionsParams{ImageName: image.Name})
	if err != nil {
		return fmt.Errorf("incus materialization: list versions for image %q: %w", image.Name, err)
	}
	aliases, err := catalogReader.ListAliases(ctx, catalog.ListAliasesParams{ImageName: image.Name})
	if err != nil {
		return fmt.Errorf("incus materialization: list aliases for image %q: %w", image.Name, err)
	}

	for _, version := range versions {
		manifest, err := catalogReader.GetVersionManifest(ctx, catalog.GetVersionManifestParams{
			ImageName: image.Name,
			Version:   version.Version,
		})
		if err != nil {
			return fmt.Errorf(
				"incus materialization: load manifest for image %q version %q: %w",
				image.Name,
				version.Version,
				err,
			)
		}
		if err := projection.projectManifest(ctx, productFile, manifest, aliases); err != nil {
			return err
		}
	}

	return nil
}

type projectionState struct {
	blobs          BlobReader
	combinedSHA256 map[combinedKey]string
}

type combinedKey struct {
	metadata catalog.Digest
	disk     catalog.Digest
}

func (projection *projectionState) projectManifest(
	ctx context.Context,
	productFile *simplestreams.ProductFile,
	manifest catalog.Manifest,
	aliases []catalog.Alias,
) error {
	for _, manifestArtifact := range manifest.Artifacts {
		artifact := manifestArtifact.Artifact
		if artifact.Format != catalog.ArtifactFormatQCOW2 {
			continue
		}
		metadataAttachment, ok := findMetadataAttachment(manifestArtifact.Attachments)
		if !ok {
			continue
		}

		productName := productName(manifest.Image.Name, manifest.Version.Version, artifact.Architecture)
		if _, exists := productFile.Products[productName]; exists {
			return fmt.Errorf("incus materialization: duplicate product %q", productName)
		}

		product := productFile.SetProduct(productName, nil)
		product.SetMetadata("aliases", productAliases(manifest.Image.Name, manifest.Version.Version, aliases))
		product.SetMetadata("arch", incusArchitecture(artifact.Architecture))
		product.SetMetadata("os", artifact.OperatingSystem)
		product.SetMetadata("release", manifest.Version.Version)
		product.SetMetadata("release_title", releaseTitle(manifest.Image, manifest.Version))
		product.SetMetadata("variant", defaultVariant)
		product.SetMetadata("requirements", map[string]any{})

		version := product.SetVersion(incusSerial(manifest.Version), nil)
		if err := projection.setItems(ctx, version, manifest, artifact, metadataAttachment); err != nil {
			return err
		}
	}

	return nil
}

func (projection *projectionState) setItems(
	ctx context.Context,
	version *simplestreams.Version,
	manifest catalog.Manifest,
	artifact catalog.Artifact,
	metadata catalog.Attachment,
) error {
	metadataPath, err := attachmentDownloadPath(manifest.Image.Name, manifest.Version.Version, artifact.ID, metadata.ID)
	if err != nil {
		return err
	}
	diskPath, err := artifactDownloadPath(manifest.Image.Name, manifest.Version.Version, artifact.ID)
	if err != nil {
		return err
	}

	metadataSHA256, err := digestSHA256Hex(metadata.BlobDigest)
	if err != nil {
		return err
	}
	diskSHA256, err := digestSHA256Hex(artifact.PrimaryBlobDigest)
	if err != nil {
		return err
	}
	combinedSHA256, err := projection.combinedDiskSHA256(ctx, metadata.BlobDigest, artifact.PrimaryBlobDigest)
	if err != nil {
		return err
	}

	metadataSize := metadata.BlobSizeBytes
	metadataItem := version.SetItem(metadataItemName, nil)
	metadataItem.FileType = metadataFileType
	metadataItem.Path = metadataPath
	metadataItem.Size = &metadataSize
	metadataItem.SHA256 = metadataSHA256
	metadataItem.SetMetadata("combined_disk-kvm-img_sha256", combinedSHA256)

	diskSize := artifact.PrimaryBlobSizeBytes
	diskItem := version.SetItem(diskItemName, nil)
	diskItem.FileType = diskFileType
	diskItem.Path = diskPath
	diskItem.Size = &diskSize
	diskItem.SHA256 = diskSHA256

	return nil
}

func (projection *projectionState) combinedDiskSHA256(
	ctx context.Context,
	metadataDigest catalog.Digest,
	diskDigest catalog.Digest,
) (string, error) {
	key := combinedKey{metadata: metadataDigest, disk: diskDigest}
	if value, ok := projection.combinedSHA256[key]; ok {
		return value, nil
	}

	metadataReader, err := projection.openBlob(ctx, metadataDigest)
	if err != nil {
		return "", err
	}
	defer func() { _ = metadataReader.Body.Close() }()

	diskReader, err := projection.openBlob(ctx, diskDigest)
	if err != nil {
		return "", err
	}
	defer func() { _ = diskReader.Body.Close() }()

	value, err := simplestreams.SHA256Concat(metadataReader.Body, diskReader.Body)
	if err != nil {
		return "", fmt.Errorf("incus materialization: compute combined disk checksum: %w", err)
	}
	projection.combinedSHA256[key] = value

	return value, nil
}

func (projection *projectionState) openBlob(
	ctx context.Context,
	digest catalog.Digest,
) (objectstore.ObjectReader, error) {
	reader, err := projection.blobs.OpenBlob(ctx, cas.OpenBlobParams{
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

func productName(imageName string, version string, architecture string) string {
	return strings.Join([]string{
		imageName,
		version,
		incusArchitecture(architecture),
		defaultVariant,
	}, ":")
}

func incusArchitecture(architecture string) string {
	switch architecture {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return architecture
	}
}

func productAliases(imageName string, version string, aliases []catalog.Alias) string {
	values := []string{imageName + "/" + version}
	seen := map[string]struct{}{values[0]: {}}
	for _, alias := range aliases {
		if alias.Version != version {
			continue
		}
		value := imageName + "/" + alias.Alias
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values[1:])

	return strings.Join(values, ",")
}

func releaseTitle(image catalog.Image, version catalog.Version) string {
	if image.DisplayName != nil && strings.TrimSpace(*image.DisplayName) != "" {
		return *image.DisplayName
	}

	return image.Name + " " + version.Version
}

func incusSerial(version catalog.Version) string {
	timestamp := version.CreatedAt
	if version.PublishedAt != nil {
		timestamp = *version.PublishedAt
	}

	return timestamp.UTC().Format("20060102_15:04")
}

func artifactDownloadPath(
	imageName string,
	version string,
	artifactID uuid.UUID,
) (simplestreams.RelativePath, error) {
	return parseProjectionPath(
		"v1/images/" + url.PathEscape(imageName) +
			"/versions/" + url.PathEscape(version) +
			"/artifacts/" + url.PathEscape(artifactID.String()) +
			"/download",
	)
}

func attachmentDownloadPath(
	imageName string,
	version string,
	artifactID uuid.UUID,
	attachmentID uuid.UUID,
) (simplestreams.RelativePath, error) {
	return parseProjectionPath(
		"v1/images/" + url.PathEscape(imageName) +
			"/versions/" + url.PathEscape(version) +
			"/artifacts/" + url.PathEscape(artifactID.String()) +
			"/attachments/" + url.PathEscape(attachmentID.String()) +
			"/download",
	)
}

func parseProjectionPath(path string) (simplestreams.RelativePath, error) {
	relativePath, err := simplestreams.ParseRelativePath(path)
	if err != nil {
		return "", fmt.Errorf("incus materialization: invalid projection path %q: %w", path, err)
	}

	return relativePath, nil
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

func (service *Service) dependencies() (Catalog, BlobReader, error) {
	if service == nil || service.catalog == nil || service.blobs == nil {
		return nil, nil, errors.New("incus materialization dependencies are not configured")
	}

	return service.catalog, service.blobs, nil
}
