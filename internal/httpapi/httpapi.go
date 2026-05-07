package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/meigma/imgsrv/internal/auth"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/telemetry"
	"github.com/meigma/imgsrv/internal/uploads"
)

// defaultUploadTTL is the upload session lifetime applied when Dependencies.UploadTTL is zero.
const defaultUploadTTL = 24 * time.Hour

// Dependencies contains adapters used by the HTTP API.
type Dependencies struct {
	// Logger receives HTTP adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger

	// Telemetry instruments HTTP requests. Nil disables OpenTelemetry wrapping.
	Telemetry *telemetry.Telemetry

	// Readiness reports whether the service can accept operational traffic.
	Readiness ReadinessChecker

	// Auth coordinates bearer authentication and action authorization. Nil leaves protected routes unavailable.
	Auth AuthService

	// Uploads coordinates client-facing upload operations. Nil leaves upload routes unavailable.
	Uploads UploadService

	// Catalog coordinates client-facing image catalog operations. Nil leaves catalog routes unavailable.
	Catalog CatalogService

	// Blobs coordinates client-facing raw CAS blob reads. Nil leaves blob routes unavailable.
	Blobs BlobService

	// Now returns the current time for upload HTTP policy. Nil selects time.Now.
	Now func() time.Time

	// UploadTTL is added to Now when creating upload sessions. Zero selects 24h.
	UploadTTL time.Duration
}

// ReadinessChecker reports whether the service can accept operational traffic.
type ReadinessChecker interface {
	// CheckReady returns nil when the service can accept operational traffic.
	CheckReady(context.Context) error
}

// ReadinessFunc adapts a function into a ReadinessChecker.
type ReadinessFunc func(context.Context) error

// CheckReady calls f(ctx).
func (f ReadinessFunc) CheckReady(ctx context.Context) error {
	return f(ctx)
}

// AuthService coordinates bearer authentication for HTTP callers.
type AuthService interface {
	// AuthenticateToken validates a raw bearer token, records successful use, and returns a principal.
	AuthenticateToken(context.Context, auth.AuthenticateTokenParams) (auth.Principal, error)
}

// UploadService coordinates upload operations for HTTP callers.
type UploadService interface {
	// BeginUpload starts a new upload session.
	BeginUpload(context.Context, uploads.BeginUploadParams) (uploads.BeginUploadResult, error)

	// PutUploadPart stores or replaces one upload part.
	PutUploadPart(context.Context, uploads.PutUploadPartParams) (uploads.Part, error)

	// CompleteUpload completes a staged multipart upload.
	CompleteUpload(context.Context, uploads.CompleteUploadParams) (uploads.Session, error)

	// AbortUpload aborts a mutable upload session.
	AbortUpload(context.Context, uploads.AbortUploadParams) (uploads.Session, error)

	// GetUpload returns current durable upload state.
	GetUpload(context.Context, uploads.GetUploadParams) (uploads.Session, error)
}

// CatalogService coordinates catalog operations for HTTP callers.
type CatalogService interface {
	// CreateImage creates an operator-defined image namespace.
	CreateImage(context.Context, catalog.CreateImageParams) (catalog.Image, error)

	// ListImages returns image namespaces with published versions in stable order.
	ListImages(context.Context, catalog.ListImagesParams) ([]catalog.Image, error)

	// GetImage returns one image namespace by name when it has a published version.
	GetImage(context.Context, catalog.GetImageParams) (catalog.Image, error)

	// CreateDraftVersion creates a mutable draft version for an image.
	CreateDraftVersion(context.Context, catalog.CreateDraftVersionParams) (catalog.Version, error)

	// ListVersions returns published versions for one image in stable order.
	ListVersions(context.Context, catalog.ListVersionsParams) ([]catalog.Version, error)

	// ListPublishedArtifacts returns artifacts for an exact published image version.
	ListPublishedArtifacts(
		context.Context,
		catalog.ListPublishedArtifactsParams,
	) ([]catalog.Artifact, error)

	// GetPublishedArtifact returns one artifact for an exact published image version.
	GetPublishedArtifact(
		context.Context,
		catalog.GetPublishedArtifactParams,
	) (catalog.Artifact, error)

	// GetPublishedAttachment returns one attachment for an exact published image version artifact.
	GetPublishedAttachment(
		context.Context,
		catalog.GetPublishedAttachmentParams,
	) (catalog.Attachment, error)

	// AddArtifact adds a primary artifact on a draft version.
	AddArtifact(context.Context, catalog.AddArtifactParams) (catalog.Artifact, error)

	// AddAttachment adds a secondary attachment on a draft version.
	AddAttachment(context.Context, catalog.AddAttachmentParams) (catalog.Attachment, error)

	// DeleteArtifact removes a primary artifact from a draft version.
	DeleteArtifact(context.Context, catalog.DeleteArtifactParams) error

	// DeleteAttachment removes an attachment from a draft artifact.
	DeleteAttachment(context.Context, catalog.DeleteAttachmentParams) error

	// PublishVersion marks a draft version immutable and publishable.
	PublishVersion(context.Context, catalog.PublishVersionParams) (catalog.Version, error)

	// PutAlias creates or moves an alias to a published version.
	PutAlias(context.Context, catalog.PutAliasParams) (catalog.Alias, error)

	// ListAliases returns aliases for one image in stable order.
	ListAliases(context.Context, catalog.ListAliasesParams) ([]catalog.Alias, error)

	// GetAlias returns an alias by image and alias name.
	GetAlias(context.Context, catalog.GetAliasParams) (catalog.Alias, error)

	// DeleteAlias removes an image alias.
	DeleteAlias(context.Context, catalog.DeleteAliasParams) error

	// GetVersionManifest resolves an exact draft or published image version manifest.
	GetVersionManifest(context.Context, catalog.GetVersionManifestParams) (catalog.Manifest, error)

	// ResolveManifest resolves a published image manifest by version or alias.
	ResolveManifest(context.Context, catalog.ResolveManifestParams) (catalog.Manifest, error)
}

// BlobService coordinates raw CAS blob reads for HTTP callers.
type BlobService interface {
	// GetBlob returns trusted CAS blob metadata by digest.
	GetBlob(context.Context, cas.GetBlobParams) (cas.Blob, error)

	// OpenBlob opens a trusted CAS blob, optionally constrained to one byte range.
	OpenBlob(context.Context, cas.OpenBlobParams) (objectstore.ObjectReader, error)
}

// api carries the configured HTTP adapter state shared across handlers.
type api struct {
	logger    *slog.Logger
	readiness ReadinessChecker
	auth      AuthService
	uploads   UploadService
	catalog   CatalogService
	blobs     BlobService
	now       func() time.Time
	uploadTTL time.Duration
}

// New constructs the HTTP API handler.
func New(deps Dependencies) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	readiness := deps.Readiness
	if readiness == nil {
		readiness = ReadinessFunc(func(context.Context) error { return nil })
	}

	now := deps.Now
	if now == nil {
		now = time.Now
	}
	uploadTTL := deps.UploadTTL
	if uploadTTL == 0 {
		uploadTTL = defaultUploadTTL
	}

	api := &api{
		logger:    logger,
		readiness: readiness,
		auth:      deps.Auth,
		uploads:   deps.Uploads,
		catalog:   deps.Catalog,
		blobs:     deps.Blobs,
		now:       now,
		uploadTTL: uploadTTL,
	}

	mux := http.NewServeMux()
	api.registerRoutes(mux)

	return deps.Telemetry.WrapHTTPHandler(Chain(mux, logRequests(logger)))
}

// registerRoutes attaches API route handlers to mux.
func (a *api) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.HandleFunc("POST /v1/uploads", a.requireAction(auth.ActionContentWrite, a.beginUpload))
	mux.HandleFunc("GET /v1/uploads/{upload_id}", a.getUpload)
	mux.HandleFunc(
		"PUT /v1/uploads/{upload_id}/parts/{part_number}",
		a.requireAction(auth.ActionContentWrite, a.putUploadPart),
	)
	mux.HandleFunc(
		"POST /v1/uploads/{upload_id}/complete",
		a.requireAction(auth.ActionContentWrite, a.completeUpload),
	)
	mux.HandleFunc(
		"POST /v1/uploads/{upload_id}/abort",
		a.requireAction(auth.ActionContentWrite, a.abortUpload),
	)
	mux.HandleFunc("GET /v1/blobs/{digest}", a.getBlob)
	mux.HandleFunc("POST /v1/images", a.requireAction(auth.ActionContentWrite, a.createImage))
	mux.HandleFunc("GET /v1/images", a.listImages)
	mux.HandleFunc("GET /v1/images/{name}", a.getImage)
	mux.HandleFunc(
		"POST /v1/images/{name}/versions",
		a.requireAction(auth.ActionContentWrite, a.createDraftVersion),
	)
	mux.HandleFunc("GET /v1/images/{name}/versions", a.listVersions)
	mux.HandleFunc("GET /v1/images/{name}/versions/{version}", a.getVersionManifest)
	mux.HandleFunc("GET /v1/images/{name}/versions/{version}/artifacts", a.listPublishedArtifacts)
	mux.HandleFunc(
		"GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}",
		a.getPublishedArtifact,
	)
	mux.HandleFunc(
		"GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/download",
		a.downloadPublishedArtifact,
	)
	mux.HandleFunc(
		"POST /v1/images/{name}/versions/{version}/artifacts",
		a.requireAction(auth.ActionContentWrite, a.addArtifact),
	)
	mux.HandleFunc(
		"DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}",
		a.requireAction(auth.ActionContentWrite, a.deleteArtifact),
	)
	mux.HandleFunc(
		"POST /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments",
		a.requireAction(auth.ActionContentWrite, a.addAttachment),
	)
	mux.HandleFunc(
		"DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}",
		a.requireAction(auth.ActionContentWrite, a.deleteAttachment),
	)
	mux.HandleFunc(
		"GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}/download",
		a.downloadPublishedAttachment,
	)
	mux.HandleFunc(
		"POST /v1/images/{name}/versions/{version}/publish",
		a.requireAction(auth.ActionContentWrite, a.publishVersion),
	)
	mux.HandleFunc(
		"PUT /v1/images/{name}/aliases/{alias}",
		a.requireAction(auth.ActionContentWrite, a.putAlias),
	)
	mux.HandleFunc("GET /v1/images/{name}/aliases", a.listAliases)
	mux.HandleFunc("GET /v1/images/{name}/aliases/{alias}", a.getAlias)
	mux.HandleFunc(
		"DELETE /v1/images/{name}/aliases/{alias}",
		a.requireAction(auth.ActionContentWrite, a.deleteAlias),
	)
	mux.HandleFunc("GET /v1/images/{name}/refs/{ref}", a.resolveManifest)
}

// healthz handles GET /healthz and reports liveness with no body.
func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// readyz handles GET /readyz and delegates to the configured ReadinessChecker.
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	if err := a.readiness.CheckReady(r.Context()); err != nil {
		a.logger.Warn("readiness check failed", "error", err)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
