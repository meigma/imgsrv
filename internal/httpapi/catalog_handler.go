package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/publish"
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

// putAliasRequest is the JSON body for PUT /v1/images/{name}/aliases/{alias}.
type putAliasRequest struct {
	// Version is the published target version string.
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

// imageListResponse is the JSON wire shape for image namespace lists.
type imageListResponse struct {
	// Images are image namespaces in stable order.
	Images []imageResponse `json:"images"`
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

// versionListResponse is the JSON wire shape for image version lists.
type versionListResponse struct {
	// Versions are image versions in stable order.
	Versions []versionResponse `json:"versions"`
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

// artifactListResponse is the JSON wire shape for artifact lists.
type artifactListResponse struct {
	// Artifacts are primary artifacts in stable catalog order.
	Artifacts []artifactResponse `json:"artifacts"`
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

// aliasResponse is the JSON wire shape of an image alias.
type aliasResponse struct {
	// ID is the stable alias identity.
	ID string `json:"id"`

	// ImageID identifies the image that owns the alias.
	ImageID string `json:"image_id"`

	// Alias is the mutable pointer name.
	Alias string `json:"alias"`

	// VersionID identifies the published target version.
	VersionID string `json:"version_id"`

	// Version is the published target version string.
	Version string `json:"version"`

	// CreatedAt is when the alias was first created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when the alias target last changed.
	UpdatedAt string `json:"updated_at"`
}

// aliasListResponse is the JSON wire shape for image alias lists.
type aliasListResponse struct {
	// Aliases are image aliases in stable order.
	Aliases []aliasResponse `json:"aliases"`
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
	a.logger.InfoContext(
		r.Context(),
		"image created",
		"operation",
		"catalog.create_image",
		"request_id",
		RequestIDFromContext(r.Context()),
		"image_id",
		image.ID.String(),
		"image_name",
		image.Name,
	)

	writeJSON(w, http.StatusCreated, newImageResponse(image))
}

// listImages handles GET /v1/images and returns image namespaces with published versions.
func (a *api) listImages(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	images, err := service.ListImages(r.Context(), catalog.ListImagesParams{})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newImageListResponse(images))
}

// getImage handles GET /v1/images/{name} and returns one image namespace with a published version.
func (a *api) getImage(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	image, err := service.GetImage(r.Context(), catalog.GetImageParams{Name: r.PathValue("name")})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newImageResponse(image))
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
	a.logger.InfoContext(
		r.Context(),
		"draft version created",
		"operation",
		"catalog.create_draft_version",
		"request_id",
		RequestIDFromContext(r.Context()),
		"version_id",
		version.ID.String(),
		"image_id",
		version.ImageID.String(),
		"image_name",
		r.PathValue("name"),
		"version",
		version.Version,
		"state",
		string(version.State),
	)

	writeJSON(w, http.StatusCreated, newVersionResponse(version))
}

// listVersions handles GET /v1/images/{name}/versions and returns published image versions.
func (a *api) listVersions(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	versions, err := service.ListVersions(r.Context(), catalog.ListVersionsParams{
		ImageName: r.PathValue("name"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newVersionListResponse(versions))
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

// resolveManifest handles GET /v1/images/{name}/refs/{ref} and resolves an exact published version or alias.
func (a *api) resolveManifest(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	manifest, err := service.ResolveManifest(r.Context(), catalog.ResolveManifestParams{
		ImageName: r.PathValue("name"),
		Version:   r.PathValue("ref"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newManifestResponse(manifest))
}

// listPublishedArtifacts handles GET /v1/images/{name}/versions/{version}/artifacts.
func (a *api) listPublishedArtifacts(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	artifacts, err := service.ListPublishedArtifacts(r.Context(), catalog.ListPublishedArtifactsParams{
		ImageName: r.PathValue("name"),
		Version:   r.PathValue("version"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newArtifactListResponse(artifacts))
}

// getPublishedArtifact handles GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}.
func (a *api) getPublishedArtifact(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}
	artifactID, ok := parseArtifactIDPath(w, r)
	if !ok {
		return
	}

	artifact, err := service.GetPublishedArtifact(r.Context(), catalog.GetPublishedArtifactParams{
		ImageName:  r.PathValue("name"),
		Version:    r.PathValue("version"),
		ArtifactID: artifactID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newArtifactResponse(artifact))
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
	a.logger.InfoContext(
		r.Context(),
		"artifact added",
		"operation",
		"catalog.add_artifact",
		"request_id",
		RequestIDFromContext(r.Context()),
		"artifact_id",
		artifact.ID.String(),
		"version_id",
		artifact.VersionID.String(),
		"image_name",
		r.PathValue("name"),
		"version",
		r.PathValue("version"),
		"digest",
		artifact.PrimaryBlobDigest.String(),
		"size_bytes",
		artifact.PrimaryBlobSizeBytes,
		"format",
		string(artifact.Format),
		"operating_system",
		artifact.OperatingSystem,
		"architecture",
		artifact.Architecture,
	)

	writeJSON(w, http.StatusCreated, newArtifactResponse(artifact))
}

// deleteArtifact handles DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}.
func (a *api) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}
	artifactID, ok := parseArtifactIDPath(w, r)
	if !ok {
		return
	}

	err := service.DeleteArtifact(r.Context(), catalog.DeleteArtifactParams{
		ImageName:  r.PathValue("name"),
		Version:    r.PathValue("version"),
		ArtifactID: artifactID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"artifact deleted",
		"operation",
		"catalog.delete_artifact",
		"request_id",
		RequestIDFromContext(r.Context()),
		"image_name",
		r.PathValue("name"),
		"version",
		r.PathValue("version"),
		"artifact_id",
		artifactID.String(),
	)

	w.WriteHeader(http.StatusNoContent)
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
	a.logger.InfoContext(
		r.Context(),
		"attachment added",
		"operation",
		"catalog.add_attachment",
		"request_id",
		RequestIDFromContext(r.Context()),
		"attachment_id",
		attachment.ID.String(),
		"artifact_id",
		attachment.ArtifactID.String(),
		"image_name",
		r.PathValue("name"),
		"version",
		r.PathValue("version"),
		"digest",
		attachment.BlobDigest.String(),
		"size_bytes",
		attachment.BlobSizeBytes,
	)

	writeJSON(w, http.StatusCreated, newAttachmentResponse(attachment))
}

// deleteAttachment handles DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}.
func (a *api) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}
	artifactID, ok := parseArtifactIDPath(w, r)
	if !ok {
		return
	}
	attachmentID, ok := parseAttachmentIDPath(w, r)
	if !ok {
		return
	}

	err := service.DeleteAttachment(r.Context(), catalog.DeleteAttachmentParams{
		ImageName:    r.PathValue("name"),
		Version:      r.PathValue("version"),
		ArtifactID:   artifactID,
		AttachmentID: attachmentID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"attachment deleted",
		"operation",
		"catalog.delete_attachment",
		"request_id",
		RequestIDFromContext(r.Context()),
		"image_name",
		r.PathValue("name"),
		"version",
		r.PathValue("version"),
		"artifact_id",
		artifactID.String(),
		"attachment_id",
		attachmentID.String(),
	)

	w.WriteHeader(http.StatusNoContent)
}

// publishVersion handles POST /v1/images/{name}/versions/{version}/publish and queues a publish job.
func (a *api) publishVersion(w http.ResponseWriter, r *http.Request) {
	service, ok := a.publishService(w)
	if !ok {
		return
	}

	job, err := service.PublishVersion(r.Context(), publish.EnqueueVersionParams{
		ImageName: r.PathValue("name"),
		Version:   r.PathValue("version"),
	})
	if err != nil {
		writePublishError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"publish job queued",
		"operation",
		"catalog.publish_version",
		"request_id",
		RequestIDFromContext(r.Context()),
		"job_id",
		job.ID.String(),
		"version_id",
		job.VersionID.String(),
		"image_name",
		job.ImageName,
		"version",
		job.Version,
		"state",
		string(job.State),
	)

	writeJSON(w, http.StatusAccepted, newPublishJobResponse(job))
}

// putAlias handles PUT /v1/images/{name}/aliases/{alias} and creates or moves an alias.
func (a *api) putAlias(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	var request putAliasRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	alias, err := service.PutAlias(r.Context(), catalog.PutAliasParams{
		ImageName: r.PathValue("name"),
		Alias:     r.PathValue("alias"),
		Version:   request.Version,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"alias updated",
		"operation",
		"catalog.put_alias",
		"request_id",
		RequestIDFromContext(r.Context()),
		"alias_id",
		alias.ID.String(),
		"image_name",
		r.PathValue("name"),
		"alias",
		alias.Alias,
		"version",
		alias.Version,
		"version_id",
		alias.VersionID.String(),
	)

	writeJSON(w, http.StatusOK, newAliasResponse(alias))
}

// listAliases handles GET /v1/images/{name}/aliases and returns image aliases.
func (a *api) listAliases(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	aliases, err := service.ListAliases(r.Context(), catalog.ListAliasesParams{
		ImageName: r.PathValue("name"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newAliasListResponse(aliases))
}

// getAlias handles GET /v1/images/{name}/aliases/{alias} and returns one image alias.
func (a *api) getAlias(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	alias, err := service.GetAlias(r.Context(), catalog.GetAliasParams{
		ImageName: r.PathValue("name"),
		Alias:     r.PathValue("alias"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newAliasResponse(alias))
}

// deleteAlias handles DELETE /v1/images/{name}/aliases/{alias} and removes one image alias.
func (a *api) deleteAlias(w http.ResponseWriter, r *http.Request) {
	service, ok := a.catalogService(w)
	if !ok {
		return
	}

	err := service.DeleteAlias(r.Context(), catalog.DeleteAliasParams{
		ImageName: r.PathValue("name"),
		Alias:     r.PathValue("alias"),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"alias deleted",
		"operation",
		"catalog.delete_alias",
		"request_id",
		RequestIDFromContext(r.Context()),
		"image_name",
		r.PathValue("name"),
		"alias",
		r.PathValue("alias"),
	)

	w.WriteHeader(http.StatusNoContent)
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

// parseAttachmentIDPath extracts and validates the attachment ID path value.
//
// On failure it writes a problem response and returns false; callers should not write further.
func parseAttachmentIDPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	attachmentID, err := uuid.Parse(r.PathValue("attachment_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "attachment id must be a UUID")
		return uuid.Nil, false
	}

	return attachmentID, true
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

// newImageListResponse projects image namespaces onto their JSON wire shape.
func newImageListResponse(images []catalog.Image) imageListResponse {
	items := make([]imageResponse, 0, len(images))
	for _, image := range images {
		items = append(items, newImageResponse(image))
	}

	return imageListResponse{Images: items}
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

// newVersionListResponse projects image versions onto their JSON wire shape.
func newVersionListResponse(versions []catalog.Version) versionListResponse {
	items := make([]versionResponse, 0, len(versions))
	for _, version := range versions {
		items = append(items, newVersionResponse(version))
	}

	return versionListResponse{Versions: items}
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

// newArtifactListResponse projects release artifacts onto their JSON wire shape.
func newArtifactListResponse(artifacts []catalog.Artifact) artifactListResponse {
	items := make([]artifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, newArtifactResponse(artifact))
	}

	return artifactListResponse{Artifacts: items}
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

// newAliasResponse projects an image alias onto its JSON wire shape.
func newAliasResponse(alias catalog.Alias) aliasResponse {
	return aliasResponse{
		ID:        alias.ID.String(),
		ImageID:   alias.ImageID.String(),
		Alias:     alias.Alias,
		VersionID: alias.VersionID.String(),
		Version:   alias.Version,
		CreatedAt: formatCatalogTime(alias.CreatedAt),
		UpdatedAt: formatCatalogTime(alias.UpdatedAt),
	}
}

// newAliasListResponse projects image aliases onto their JSON wire shape.
func newAliasListResponse(aliases []catalog.Alias) aliasListResponse {
	items := make([]aliasResponse, 0, len(aliases))
	for _, alias := range aliases {
		items = append(items, newAliasResponse(alias))
	}

	return aliasListResponse{Aliases: items}
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
