package httpapi

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/uploads"
)

// downloadPublishedArtifact handles GET and HEAD /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/download.
func (a *api) downloadPublishedArtifact(w http.ResponseWriter, r *http.Request) {
	catalogService, ok := a.catalogService(w)
	if !ok {
		return
	}
	artifactID, ok := parseArtifactIDPath(w, r)
	if !ok {
		return
	}

	artifact, err := catalogService.GetPublishedArtifact(r.Context(), catalog.GetPublishedArtifactParams{
		ImageName:  r.PathValue("name"),
		Version:    r.PathValue("version"),
		ArtifactID: artifactID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	a.serveCatalogBlob(
		w,
		r,
		catalogArtifactBlob(artifact),
		slog.String("image_name", r.PathValue("name")),
		slog.String("version", r.PathValue("version")),
		slog.String("artifact_id", artifactID.String()),
	)
}

// downloadPublishedAttachment handles GET and HEAD /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}/download.
func (a *api) downloadPublishedAttachment(w http.ResponseWriter, r *http.Request) {
	catalogService, ok := a.catalogService(w)
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

	attachment, err := catalogService.GetPublishedAttachment(r.Context(), catalog.GetPublishedAttachmentParams{
		ImageName:    r.PathValue("name"),
		Version:      r.PathValue("version"),
		ArtifactID:   artifactID,
		AttachmentID: attachmentID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	a.serveCatalogBlob(
		w,
		r,
		catalogAttachmentBlob(attachment),
		slog.String("image_name", r.PathValue("name")),
		slog.String("version", r.PathValue("version")),
		slog.String("artifact_id", artifactID.String()),
		slog.String("attachment_id", attachmentID.String()),
	)
}

// serveCatalogBlob streams a catalog-scoped blob using the same HTTP behavior as raw CAS blob reads.
func (a *api) serveCatalogBlob(w http.ResponseWriter, r *http.Request, blob cas.Blob, attrs ...slog.Attr) {
	service, ok := a.blobService(w, r)
	if !ok {
		return
	}

	etag := blobETag(blob)
	modifiedAt := blob.VerifiedAt.UTC()
	if !modifiedAt.IsZero() {
		modifiedAt = modifiedAt.Truncate(blobTimePrecision)
	}

	if blobNotModified(r, etag, modifiedAt) {
		writeBlobHeaders(w, blob, etag, modifiedAt)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	rangeRequest, ok, err := selectBlobRange(r, etag, modifiedAt, blob.SizeBytes)
	if err != nil {
		w.Header().Set("Content-Range", unsatisfiedBlobRange(blob.SizeBytes))
		writeBlobProblem(w, r, http.StatusRequestedRangeNotSatisfiable, err.Error())
		return
	}

	status := http.StatusOK
	contentLength := blob.SizeBytes
	if ok {
		status = http.StatusPartialContent
		contentLength = rangeRequest.length
	}

	var byteRange = rangeRequest.openPointer(ok)
	reader, err := openBlobReader(r.Context(), service, blob.Digest, byteRange)
	if err != nil {
		writeBlobReadError(w, r, err)
		return
	}
	if r.Method == http.MethodHead {
		_ = reader.Body.Close()
		writeBlobSuccess(w, blob, etag, modifiedAt, status, contentLength, rangeRequest, ok)
		return
	}
	defer func() {
		_ = reader.Body.Close()
	}()

	writeBlobSuccess(w, blob, etag, modifiedAt, status, contentLength, rangeRequest, ok)
	if _, err := io.Copy(w, reader.Body); err != nil {
		logAttrs := []slog.Attr{
			slog.String("operation", "catalog_blob.stream"),
			slog.String("request_id", RequestIDFromContext(r.Context())),
			slog.String("digest", blob.Digest.String()),
			slog.Int("status", status),
			slog.Int64("range_start", rangeRequest.start),
			slog.Int64("range_end", rangeRequest.end),
			slog.Any("error", err),
		}
		logAttrs = append(logAttrs, attrs...)
		a.logger.LogAttrs(
			r.Context(),
			streamErrorLevel(r.Context(), err),
			"stream catalog blob response failed",
			logAttrs...)
	}
}

// catalogArtifactBlob projects an artifact declaration into blob response metadata.
func catalogArtifactBlob(artifact catalog.Artifact) cas.Blob {
	mediaType := artifact.PrimaryMediaType

	return cas.Blob{
		Digest:     uploads.Digest(artifact.PrimaryBlobDigest.String()),
		SizeBytes:  artifact.PrimaryBlobSizeBytes,
		MediaType:  &mediaType,
		VerifiedAt: artifact.UpdatedAt,
		CreatedAt:  artifact.CreatedAt,
	}
}

// catalogAttachmentBlob projects an attachment declaration into blob response metadata.
func catalogAttachmentBlob(attachment catalog.Attachment) cas.Blob {
	mediaType := attachment.MediaType

	return cas.Blob{
		Digest:     uploads.Digest(attachment.BlobDigest.String()),
		SizeBytes:  attachment.BlobSizeBytes,
		MediaType:  &mediaType,
		VerifiedAt: attachment.UpdatedAt,
		CreatedAt:  attachment.CreatedAt,
	}
}
