// Package incus projects published imgsrv catalog state as an Incus Simple Streams mirror.
package incus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	simplestreams "github.com/imgoci/go-simplestreams"
	incusschema "github.com/imgoci/go-simplestreams/schema/incus"

	"github.com/meigma/imgsrv/internal/catalog"
	safelog "github.com/meigma/imgsrv/internal/logging"
)

const (
	// ImagesProductPath is the Simple Streams product document path served by imgsrv.
	ImagesProductPath simplestreams.RelativePath = "streams/v1/images.json"

	metadataItemName = "incus.tar.xz"
	metadataFileType = "incus.tar.xz"
	diskItemName     = "disk-kvm.img"
	diskFileType     = "disk-kvm.img"
)

// ProjectionRow is one persisted Incus projection item for an eligible artifact.
type ProjectionRow struct {
	// VersionID identifies the published image version.
	VersionID uuid.UUID

	// ArtifactID identifies the qcow2 artifact.
	ArtifactID uuid.UUID

	// MetadataAttachmentID identifies the incus.tar.xz attachment.
	MetadataAttachmentID uuid.UUID

	// ImageName is the image namespace.
	ImageName string

	// ImageDisplayName is the optional human-friendly image label.
	ImageDisplayName *string

	// Version is the operator-defined version string.
	Version string

	// VersionCreatedAt is when the image version was created.
	VersionCreatedAt time.Time

	// PublishedAt is when the version became visible to published reads.
	PublishedAt *time.Time

	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string

	// Architecture is the artifact architecture token.
	Architecture string

	// Variant is the artifact variant token.
	Variant string

	// MetadataPath is the API download path for the metadata item.
	MetadataPath string

	// DiskPath is the API download path for the qcow2 disk item.
	DiskPath string

	// MetadataSHA256 is the metadata blob digest without the sha256: prefix.
	MetadataSHA256 string

	// MetadataSizeBytes is the metadata blob size.
	MetadataSizeBytes int64

	// DiskSHA256 is the qcow2 blob digest without the sha256: prefix.
	DiskSHA256 string

	// DiskSizeBytes is the qcow2 blob size.
	DiskSizeBytes int64

	// CombinedDiskKVMImgSHA256 is the Incus combined metadata-plus-disk checksum.
	CombinedDiskKVMImgSHA256 string
}

// ProjectionStore reads persisted Incus projection rows.
type ProjectionStore interface {
	// ListProjectionRows returns completed projection rows for published versions.
	ListProjectionRows(context.Context) ([]ProjectionRow, error)
}

// AliasCatalog reads current aliases for product metadata.
type AliasCatalog interface {
	// ListAliases returns aliases for one image.
	ListAliases(context.Context, catalog.ListAliasesParams) ([]catalog.Alias, error)
}

// Config configures the Incus Simple Streams projection service.
type Config struct {
	// Catalog reads mutable alias state used at render time.
	Catalog AliasCatalog

	// Store reads persisted Incus projection rows.
	Store ProjectionStore

	// Logger receives Simple Streams materialization logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Service builds Incus-compatible Simple Streams metadata from persisted projection rows.
type Service struct {
	catalog AliasCatalog
	store   ProjectionStore
	logger  *slog.Logger
}

// NewService constructs an Incus projection service.
func NewService(config Config) *Service {
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	return &Service{
		catalog: config.Catalog,
		store:   config.Store,
		logger:  logger,
	}
}

// Index renders the Simple Streams index document.
func (service *Service) Index(ctx context.Context) ([]byte, error) {
	_, store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	rows, err := store.ListProjectionRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("incus materialization: list projection rows: %w", err)
	}
	products, err := projectedProductNames(rows)
	if err != nil {
		return nil, err
	}
	service.logger.DebugContext(
		ctx,
		"incus simple streams index built",
		"operation",
		"incus.render_index",
		"projection_rows",
		len(rows),
		"product_count",
		len(products),
	)

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
	catalogReader, store, err := service.dependencies()
	if err != nil {
		return nil, err
	}

	rows, err := store.ListProjectionRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("incus materialization: list projection rows: %w", err)
	}

	productFile := simplestreams.NewProductFile(incusschema.ContentIDImages)
	productFile.DataType = incusschema.DataTypeImageDownloads
	aliases := map[string][]catalog.Alias{}
	for _, row := range rows {
		productName := productName(row.ImageName, row.Version, row.Architecture, row.Variant)
		if _, exists := productFile.Products[productName]; exists {
			return nil, fmt.Errorf("incus materialization: duplicate product %q", productName)
		}
		imageAliases, ok := aliases[row.ImageName]
		if !ok {
			var aliasErr error
			imageAliases, aliasErr = catalogReader.ListAliases(ctx, catalog.ListAliasesParams{ImageName: row.ImageName})
			if aliasErr != nil {
				return nil, fmt.Errorf("incus materialization: list aliases for image %q: %w", row.ImageName, aliasErr)
			}
			aliases[row.ImageName] = imageAliases
		}

		if err := projectRow(productFile, row, imageAliases); err != nil {
			return nil, err
		}
	}
	service.logger.DebugContext(
		ctx,
		"incus simple streams product file built",
		"operation",
		"incus.render_product_file",
		"projection_rows",
		len(rows),
		"product_count",
		len(productFile.Products),
		"alias_image_count",
		len(aliases),
	)

	return productFile, nil
}

func projectedProductNames(rows []ProjectionRow) ([]string, error) {
	names := []string{}
	seen := map[string]struct{}{}
	for _, row := range rows {
		name := productName(row.ImageName, row.Version, row.Architecture, row.Variant)
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("incus materialization: duplicate product %q", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)

	return names, nil
}

func projectRow(
	productFile *simplestreams.ProductFile,
	row ProjectionRow,
	aliases []catalog.Alias,
) error {
	variant := catalog.NormalizeArtifactVariant(row.Variant)
	product := productFile.SetProduct(productName(row.ImageName, row.Version, row.Architecture, variant), nil)
	product.SetMetadata("aliases", productAliases(row.ImageName, row.Version, aliases))
	product.SetMetadata("arch", incusArchitecture(row.Architecture))
	product.SetMetadata("os", row.OperatingSystem)
	product.SetMetadata("release", row.Version)
	product.SetMetadata("release_title", releaseTitle(row.ImageName, row.ImageDisplayName, row.Version))
	product.SetMetadata("variant", variant)
	product.SetMetadata("requirements", map[string]any{})

	version := product.SetVersion(incusSerial(row), nil)
	if err := setItems(version, row); err != nil {
		return err
	}

	return nil
}

func setItems(version *simplestreams.Version, row ProjectionRow) error {
	metadataPath, err := parseProjectionPath(row.MetadataPath)
	if err != nil {
		return err
	}
	diskPath, err := parseProjectionPath(row.DiskPath)
	if err != nil {
		return err
	}

	metadataSize := row.MetadataSizeBytes
	metadataItem := version.SetItem(metadataItemName, nil)
	metadataItem.FileType = metadataFileType
	metadataItem.Path = metadataPath
	metadataItem.Size = &metadataSize
	metadataItem.SHA256 = row.MetadataSHA256
	metadataItem.SetMetadata("combined_disk-kvm-img_sha256", row.CombinedDiskKVMImgSHA256)

	diskSize := row.DiskSizeBytes
	diskItem := version.SetItem(diskItemName, nil)
	diskItem.FileType = diskFileType
	diskItem.Path = diskPath
	diskItem.Size = &diskSize
	diskItem.SHA256 = row.DiskSHA256

	return nil
}

func productName(imageName string, version string, architecture string, variant string) string {
	return strings.Join([]string{
		imageName,
		version,
		incusArchitecture(architecture),
		catalog.NormalizeArtifactVariant(variant),
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

func releaseTitle(imageName string, displayName *string, version string) string {
	if displayName != nil && strings.TrimSpace(*displayName) != "" {
		return *displayName
	}

	return imageName + " " + version
}

func incusSerial(row ProjectionRow) string {
	timestamp := row.VersionCreatedAt
	if row.PublishedAt != nil {
		timestamp = *row.PublishedAt
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

func (service *Service) dependencies() (AliasCatalog, ProjectionStore, error) {
	if service == nil || service.catalog == nil || service.store == nil {
		return nil, nil, errors.New("incus materialization dependencies are not configured")
	}

	return service.catalog, service.store, nil
}
