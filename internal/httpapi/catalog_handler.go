package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/catalog"
)

// errCatalogServiceUnavailable signals that catalog routes were called without a configured CatalogService.
var errCatalogServiceUnavailable = errors.New("catalog service is not configured")

// createImageRequest is the JSON body for POST /v1/images.
type createImageRequest struct {
	// Name is the operator-defined unique image name.
	Name string `json:"name"`

	// DisplayName is an optional human-friendly image label.
	DisplayName *string `json:"display_name,omitempty"`

	// Description is optional operator-facing image description text.
	Description *string `json:"description,omitempty"`
}

// createDraftVersionRequest is the JSON body for POST /v1/images/{name}/versions.
type createDraftVersionRequest struct {
	// Version is the operator-defined version string.
	Version string `json:"version"`
}

// addArtifactRequest is the JSON body for POST /v1/images/{name}/versions/{version}/artifacts.
type addArtifactRequest struct {
	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string `json:"operating_system"`

	// Architecture is the artifact architecture token.
	Architecture string `json:"architecture"`

	// Format is the primary artifact format.
	Format string `json:"format"`

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest string `json:"primary_blob_digest"`

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64 `json:"primary_blob_size_bytes"`

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string `json:"primary_media_type"`
}

// addAttachmentRequest is the JSON body for POST /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments.
type addAttachmentRequest struct {
	// Name is the attachment role or name.
	Name string `json:"name"`

	// MediaType is the media type for the attachment blob.
	MediaType string `json:"media_type"`

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest string `json:"blob_digest"`

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64 `json:"blob_size_bytes"`
}

// imageResponse is the JSON wire shape of an image namespace.
type imageResponse struct {
	// ID is the stable image identity.
	ID string `json:"id"`

	// Name is the operator-defined unique image name.
	Name string `json:"name"`

	// DisplayName is an optional human-friendly image label.
	DisplayName *string `json:"display_name,omitempty"`

	// Description is optional operator-facing image description text.
	Description *string `json:"description,omitempty"`

	// CreatedAt is when the image was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when mutable image metadata last changed.
	UpdatedAt string `json:"updated_at"`
}

// versionResponse is the JSON wire shape of an image version.
type versionResponse struct {
	// ID is the stable image-version identity.
	ID string `json:"id"`

	// ImageID identifies the parent image.
	ImageID string `json:"image_id"`

	// Version is the operator-defined version string.
	Version string `json:"version"`

	// State is the draft or published lifecycle state.
	State catalog.VersionState `json:"state"`

	// PublishedAt is set when the version becomes immutable.
	PublishedAt *string `json:"published_at,omitempty"`

	// CreatedAt is when the version was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when mutable version metadata last changed.
	UpdatedAt string `json:"updated_at"`
}

// artifactResponse is the JSON wire shape of a primary release artifact.
type artifactResponse struct {
	// ID is the stable artifact identity.
	ID string `json:"id"`

	// VersionID identifies the parent image version.
	VersionID string `json:"version_id"`

	// OperatingSystem is the artifact operating-system token.
	OperatingSystem string `json:"operating_system"`

	// Architecture is the artifact architecture token.
	Architecture string `json:"architecture"`

	// Format is the primary artifact format.
	Format catalog.ArtifactFormat `json:"format"`

	// PrimaryBlobDigest is the digest of the primary CAS blob.
	PrimaryBlobDigest string `json:"primary_blob_digest"`

	// PrimaryBlobSizeBytes is the expected size of the primary CAS blob.
	PrimaryBlobSizeBytes int64 `json:"primary_blob_size_bytes"`

	// PrimaryMediaType is the media type for the primary blob in this manifest.
	PrimaryMediaType string `json:"primary_media_type"`

	// CreatedAt is when the artifact declaration was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when the artifact declaration last changed.
	UpdatedAt string `json:"updated_at"`
}

// attachmentResponse is the JSON wire shape of an artifact attachment.
type attachmentResponse struct {
	// ID is the stable attachment identity.
	ID string `json:"id"`

	// ArtifactID identifies the parent artifact.
	ArtifactID string `json:"artifact_id"`

	// Name is the attachment role or name.
	Name string `json:"name"`

	// MediaType is the media type for the attachment blob.
	MediaType string `json:"media_type"`

	// BlobDigest is the digest of the attachment CAS blob.
	BlobDigest string `json:"blob_digest"`

	// BlobSizeBytes is the expected size of the attachment CAS blob.
	BlobSizeBytes int64 `json:"blob_size_bytes"`

	// CreatedAt is when the attachment declaration was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when the attachment declaration last changed.
	UpdatedAt string `json:"updated_at"`
}

// manifestResponse is the JSON wire shape of an image version manifest.
type manifestResponse struct {
	// Image is the image namespace for the manifest.
	Image imageResponse `json:"image"`

	// Version is the draft or published version represented by the manifest.
	Version versionResponse `json:"version"`

	// Artifacts are the primary artifacts in stable catalog order.
	Artifacts []manifestArtifactResponse `json:"artifacts"`
}

// manifestArtifactResponse groups a primary artifact with its attachments.
type manifestArtifactResponse struct {
	// Artifact is the primary release artifact.
	Artifact artifactResponse `json:"artifact"`

	// Attachments are artifact attachments in stable catalog order.
	Attachments []attachmentResponse `json:"attachments"`
}

// createImage handles POST /v1/images and creates an image namespace.
func (a *api) createImage(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	var request createImageRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	image, err := service.CreateImage(r.Context(), catalog.CreateImageParams{
		Name:        request.Name,
		DisplayName: request.DisplayName,
		Description: request.Description,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newImageResponse(image))
}

// createDraftVersion handles POST /v1/images/{name}/versions and creates a draft version.
func (a *api) createDraftVersion(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	var request createDraftVersionRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	version, err := service.CreateDraftVersion(r.Context(), catalog.CreateDraftVersionParams{
		ImageName: r.PathValue("name"),
		Version:   request.Version,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newVersionResponse(version))
}

// getVersionManifest handles GET /v1/images/{name}/versions/{version} and returns an exact version manifest.
func (a *api) getVersionManifest(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	manifest, err := service.GetVersionManifest(r.Context(), catalog.GetVersionManifestParams{
		ImageName: r.PathValue("name"),
		Version:   r.PathValue("version"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newManifestResponse(manifest))
}

// addArtifact handles POST /v1/images/{name}/versions/{version}/artifacts and adds a draft artifact.
func (a *api) addArtifact(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	var request addArtifactRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	digest, err := catalog.ParseDigest(request.PrimaryBlobDigest)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	artifact, err := service.AddArtifact(r.Context(), catalog.AddArtifactParams{
		ImageName:            r.PathValue("name"),
		Version:              r.PathValue("version"),
		OperatingSystem:      request.OperatingSystem,
		Architecture:         request.Architecture,
		Format:               catalog.ArtifactFormat(request.Format),
		PrimaryBlobDigest:    digest,
		PrimaryBlobSizeBytes: request.PrimaryBlobSizeBytes,
		PrimaryMediaType:     request.PrimaryMediaType,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newArtifactResponse(artifact))
}

// addAttachment handles POST /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments.
func (a *api) addAttachment(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}
	artifactID, ok := parseArtifactIDPath(w, r)
	if !ok {
		return
	}

	var request addAttachmentRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	digest, err := catalog.ParseDigest(request.BlobDigest)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	attachment, err := service.AddAttachment(r.Context(), catalog.AddAttachmentParams{
		ImageName:     r.PathValue("name"),
		Version:       r.PathValue("version"),
		ArtifactID:    artifactID,
		Name:          request.Name,
		MediaType:     request.MediaType,
		BlobDigest:    digest,
		BlobSizeBytes: request.BlobSizeBytes,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, newAttachmentResponse(attachment))
}

// publishVersion handles POST /v1/images/{name}/versions/{version}/publish and publishes a draft version.
func (a *api) publishVersion(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	version, err := service.PublishVersion(r.Context(), catalog.PublishVersionParams{
		ImageName: r.PathValue("name"),
		Version:   r.PathValue("version"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newVersionResponse(version))
}

// catalogService returns the configured CatalogService or writes a 503 problem and reports false.
func (a *api) catalogService(w http.ResponseWriter) (CatalogService, bool) {
	if a.catalog == nil {
		writeProblem(w, http.StatusServiceUnavailable, errCatalogServiceUnavailable.Error())
		return nil, false
	}

	return a.catalog, true
}

// parseArtifactIDPath extracts and validates the artifact ID path value.
//
// On failure it writes a problem response and returns false; callers should not write further.
func parseArtifactIDPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	artifactID, err := uuid.Parse(r.PathValue("artifact_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "artifact id must be a UUID")
		return uuid.Nil, false
	}

	return artifactID, true
}

// newImageResponse projects an image namespace onto its JSON wire shape.
func newImageResponse(image catalog.Image) imageResponse {
	return imageResponse{
		ID:          image.ID.String(),
		Name:        image.Name,
		DisplayName: image.DisplayName,
		Description: image.Description,
		CreatedAt:   formatCatalogTime(image.CreatedAt),
		UpdatedAt:   formatCatalogTime(image.UpdatedAt),
	}
}

// newVersionResponse projects an image version onto its JSON wire shape.
func newVersionResponse(version catalog.Version) versionResponse {
	return versionResponse{
		ID:          version.ID.String(),
		ImageID:     version.ImageID.String(),
		Version:     version.Version,
		State:       version.State,
		PublishedAt: formatOptionalCatalogTime(version.PublishedAt),
		CreatedAt:   formatCatalogTime(version.CreatedAt),
		UpdatedAt:   formatCatalogTime(version.UpdatedAt),
	}
}

// newArtifactResponse projects a release artifact onto its JSON wire shape.
func newArtifactResponse(artifact catalog.Artifact) artifactResponse {
	return artifactResponse{
		ID:                   artifact.ID.String(),
		VersionID:            artifact.VersionID.String(),
		OperatingSystem:      artifact.OperatingSystem,
		Architecture:         artifact.Architecture,
		Format:               artifact.Format,
		PrimaryBlobDigest:    artifact.PrimaryBlobDigest.String(),
		PrimaryBlobSizeBytes: artifact.PrimaryBlobSizeBytes,
		PrimaryMediaType:     artifact.PrimaryMediaType,
		CreatedAt:            formatCatalogTime(artifact.CreatedAt),
		UpdatedAt:            formatCatalogTime(artifact.UpdatedAt),
	}
}

// newAttachmentResponse projects an artifact attachment onto its JSON wire shape.
func newAttachmentResponse(attachment catalog.Attachment) attachmentResponse {
	return attachmentResponse{
		ID:            attachment.ID.String(),
		ArtifactID:    attachment.ArtifactID.String(),
		Name:          attachment.Name,
		MediaType:     attachment.MediaType,
		BlobDigest:    attachment.BlobDigest.String(),
		BlobSizeBytes: attachment.BlobSizeBytes,
		CreatedAt:     formatCatalogTime(attachment.CreatedAt),
		UpdatedAt:     formatCatalogTime(attachment.UpdatedAt),
	}
}

// newManifestResponse projects a catalog manifest onto its JSON wire shape.
func newManifestResponse(manifest catalog.Manifest) manifestResponse {
	artifacts := make([]manifestArtifactResponse, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		attachments := make([]attachmentResponse, 0, len(artifact.Attachments))
		for _, attachment := range artifact.Attachments {
			attachments = append(attachments, newAttachmentResponse(attachment))
		}
		artifacts = append(artifacts, manifestArtifactResponse{
			Artifact:    newArtifactResponse(artifact.Artifact),
			Attachments: attachments,
		})
	}

	return manifestResponse{
		Image:     newImageResponse(manifest.Image),
		Version:   newVersionResponse(manifest.Version),
		Artifacts: artifacts,
	}
}

// formatCatalogTime returns the timestamp format used in catalog JSON responses.
func formatCatalogTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

// formatOptionalCatalogTime returns nil for unset optional timestamps or the catalog JSON timestamp otherwise.
func formatOptionalCatalogTime(value *time.Time) *string {
	if value == nil {
		return nil
	}

	formatted := formatCatalogTime(*value)
	return &formatted
}
