package client

import (
	"context"
	"net/http"
	"net/url"
)

// CatalogClient provides image catalog API operations.
type CatalogClient interface {
	// CreateImage creates an image namespace.
	CreateImage(context.Context, CreateImageRequest) (Image, error)

	// CreateDraftVersion creates a mutable draft version for an image.
	CreateDraftVersion(context.Context, string, CreateDraftVersionRequest) (ImageVersion, error)

	// GetVersionManifest returns an exact draft or published version manifest.
	GetVersionManifest(context.Context, string, string) (Manifest, error)

	// AddArtifact adds a primary artifact on a draft version.
	AddArtifact(context.Context, string, string, AddArtifactRequest) (Artifact, error)

	// AddAttachment adds a secondary attachment on a draft artifact.
	AddAttachment(context.Context, string, string, string, AddAttachmentRequest) (Attachment, error)

	// PublishVersion publishes a draft version.
	PublishVersion(context.Context, string, string) (ImageVersion, error)
}

// HTTPCatalogClient is the concrete HTTP implementation of CatalogClient.
type HTTPCatalogClient struct {
	// transport carries the HTTP configuration shared with the parent Client.
	transport *transport
}

var _ CatalogClient = (*HTTPCatalogClient)(nil)

// CreateImageRequest creates an image namespace.
type CreateImageRequest struct {
	// Name is the operator-defined unique image name.
	Name string `json:"name"`

	// DisplayName is an optional human-friendly image label.
	DisplayName *string `json:"display_name,omitempty"`

	// Description is optional operator-facing image description text.
	Description *string `json:"description,omitempty"`
}

// CreateDraftVersionRequest creates a mutable draft version.
type CreateDraftVersionRequest struct {
	// Version is the operator-defined version string.
	Version string `json:"version"`
}

// AddArtifactRequest adds a primary artifact to a draft version.
type AddArtifactRequest struct {
	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string `json:"operating_system"`

	// Architecture is the artifact architecture token.
	Architecture string `json:"architecture"`

	// Format is the primary artifact format.
	Format ArtifactFormat `json:"format"`

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest Digest `json:"primary_blob_digest"`

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64 `json:"primary_blob_size_bytes"`

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string `json:"primary_media_type"`
}

// AddAttachmentRequest adds an attachment blob to a draft artifact.
type AddAttachmentRequest struct {
	// Name is the attachment role or name.
	Name string `json:"name"`

	// MediaType is the media type for the attachment blob.
	MediaType string `json:"media_type"`

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest Digest `json:"blob_digest"`

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64 `json:"blob_size_bytes"`
}

// Image is an operator-defined image namespace.
type Image struct {
	// ID is the stable image identity.
	ID string `json:"id"`

	// Name is the operator-defined unique image name.
	Name string `json:"name"`

	// DisplayName is an optional human-friendly image label.
	DisplayName *string `json:"display_name,omitempty"`

	// Description is optional operator-facing image description text.
	Description *string `json:"description,omitempty"`

	// CreatedAt is the RFC3339 timestamp when the image was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the RFC3339 timestamp when mutable image metadata last changed.
	UpdatedAt string `json:"updated_at"`
}

// ImageVersion is a draft or published version of an image.
type ImageVersion struct {
	// ID is the stable image-version identity.
	ID string `json:"id"`

	// ImageID identifies the parent image.
	ImageID string `json:"image_id"`

	// Version is the operator-defined version string.
	Version string `json:"version"`

	// State is the draft or published lifecycle state.
	State ImageVersionState `json:"state"`

	// PublishedAt is set when the version becomes immutable.
	PublishedAt *string `json:"published_at,omitempty"`

	// CreatedAt is the RFC3339 timestamp when the version was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the RFC3339 timestamp when mutable version metadata last changed.
	UpdatedAt string `json:"updated_at"`
}

// Artifact describes a primary release artifact on a version.
type Artifact struct {
	// ID is the stable artifact identity.
	ID ArtifactID `json:"id"`

	// VersionID identifies the parent image version.
	VersionID string `json:"version_id"`

	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string `json:"operating_system"`

	// Architecture is the artifact architecture token.
	Architecture string `json:"architecture"`

	// Format is the primary artifact format.
	Format ArtifactFormat `json:"format"`

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest Digest `json:"primary_blob_digest"`

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64 `json:"primary_blob_size_bytes"`

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string `json:"primary_media_type"`

	// CreatedAt is the RFC3339 timestamp when the artifact declaration was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the RFC3339 timestamp when the artifact declaration last changed.
	UpdatedAt string `json:"updated_at"`
}

// Attachment describes a blob attached to a release artifact.
type Attachment struct {
	// ID is the stable attachment identity.
	ID string `json:"id"`

	// ArtifactID identifies the parent artifact.
	ArtifactID ArtifactID `json:"artifact_id"`

	// Name is the attachment role or name.
	Name string `json:"name"`

	// MediaType is the media type for the attachment blob.
	MediaType string `json:"media_type"`

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest Digest `json:"blob_digest"`

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64 `json:"blob_size_bytes"`

	// CreatedAt is the RFC3339 timestamp when the attachment declaration was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the RFC3339 timestamp when the attachment declaration last changed.
	UpdatedAt string `json:"updated_at"`
}

// Manifest is the catalog view for an exact image version.
type Manifest struct {
	// Image is the image namespace for the manifest.
	Image Image `json:"image"`

	// Version is the draft or published version represented by the manifest.
	Version ImageVersion `json:"version"`

	// Artifacts are the primary artifacts in stable catalog order.
	Artifacts []ManifestArtifact `json:"artifacts"`
}

// ManifestArtifact groups a primary artifact with its attachments.
type ManifestArtifact struct {
	// Artifact is the primary release artifact.
	Artifact Artifact `json:"artifact"`

	// Attachments are artifact attachments in stable catalog order.
	Attachments []Attachment `json:"attachments"`
}

// newHTTPCatalogClient binds a catalog operation group to the shared transport.
func newHTTPCatalogClient(transport *transport) *HTTPCatalogClient {
	return &HTTPCatalogClient{transport: transport}
}

// CreateImage creates an image namespace.
func (client *HTTPCatalogClient) CreateImage(ctx context.Context, request CreateImageRequest) (Image, error) {
	var image Image
	err := client.transport.doJSON(ctx, "/v1/images", request, http.StatusCreated, &image)

	return image, err
}

// CreateDraftVersion creates a mutable draft version for an image.
func (client *HTTPCatalogClient) CreateDraftVersion(
	ctx context.Context,
	imageName string,
	request CreateDraftVersionRequest,
) (ImageVersion, error) {
	var version ImageVersion
	path := "/v1/images/" + url.PathEscape(imageName) + "/versions"
	err := client.transport.doJSON(ctx, path, request, http.StatusCreated, &version)

	return version, err
}

// GetVersionManifest returns an exact draft or published version manifest.
func (client *HTTPCatalogClient) GetVersionManifest(
	ctx context.Context,
	imageName string,
	version string,
) (Manifest, error) {
	var manifest Manifest
	path := versionPath(imageName, version)
	err := client.transport.do(ctx, http.MethodGet, path, nil, 0, nil, http.StatusOK, &manifest)

	return manifest, err
}

// AddArtifact adds a primary artifact on a draft version.
func (client *HTTPCatalogClient) AddArtifact(
	ctx context.Context,
	imageName string,
	version string,
	request AddArtifactRequest,
) (Artifact, error) {
	var artifact Artifact
	path := versionPath(imageName, version) + "/artifacts"
	err := client.transport.doJSON(ctx, path, request, http.StatusCreated, &artifact)

	return artifact, err
}

// AddAttachment adds a secondary attachment on a draft artifact.
func (client *HTTPCatalogClient) AddAttachment(
	ctx context.Context,
	imageName string,
	version string,
	artifactID string,
	request AddAttachmentRequest,
) (Attachment, error) {
	var attachment Attachment
	path := versionPath(imageName, version) + "/artifacts/" + url.PathEscape(artifactID) + "/attachments"
	err := client.transport.doJSON(ctx, path, request, http.StatusCreated, &attachment)

	return attachment, err
}

// PublishVersion publishes a draft version.
func (client *HTTPCatalogClient) PublishVersion(
	ctx context.Context,
	imageName string,
	version string,
) (ImageVersion, error) {
	var published ImageVersion
	path := versionPath(imageName, version) + "/publish"
	err := client.transport.do(ctx, http.MethodPost, path, nil, 0, nil, http.StatusOK, &published)

	return published, err
}

// versionPath returns the API path for an exact image version.
func versionPath(imageName string, version string) string {
	return "/v1/images/" + url.PathEscape(imageName) + "/versions/" + url.PathEscape(version)
}
