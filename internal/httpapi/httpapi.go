package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/meigma/imgsrv/internal/telemetry"
	"github.com/meigma/imgsrv/internal/uploads"
)

const defaultUploadTTL = 24 * time.Hour

// Dependencies contains adapters used by the HTTP API.
type Dependencies struct {
	// Logger receives HTTP adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger

	// Telemetry instruments HTTP requests. Nil disables OpenTelemetry wrapping.
	Telemetry *telemetry.Telemetry

	// Readiness reports whether the service can accept operational traffic.
	Readiness ReadinessChecker

	// Uploads coordinates client-facing upload operations. Nil leaves upload routes unavailable.
	Uploads UploadService

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

// UploadService coordinates upload operations for HTTP callers.
type UploadService interface {
	// BeginUpload starts a new upload session.
	BeginUpload(context.Context, uploads.BeginUploadParams) (uploads.Session, error)

	// PutUploadPart stores or replaces one upload part.
	PutUploadPart(context.Context, uploads.PutUploadPartParams) (uploads.Part, error)

	// CompleteUpload completes a staged multipart upload.
	CompleteUpload(context.Context, uploads.CompleteUploadParams) (uploads.Session, error)

	// GetUpload returns current durable upload state.
	GetUpload(context.Context, uploads.GetUploadParams) (uploads.Session, error)
}

type api struct {
	logger    *slog.Logger
	readiness ReadinessChecker
	uploads   UploadService
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
		uploads:   deps.Uploads,
		now:       now,
		uploadTTL: uploadTTL,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.healthz)
	mux.HandleFunc("GET /readyz", api.readyz)
	mux.HandleFunc("POST /v1/uploads", api.beginUpload)
	mux.HandleFunc("GET /v1/uploads/{upload_id}", api.getUpload)
	mux.HandleFunc("PUT /v1/uploads/{upload_id}/parts/{part_number}", api.putUploadPart)
	mux.HandleFunc("POST /v1/uploads/{upload_id}/complete", api.completeUpload)

	return deps.Telemetry.WrapHTTPHandler(Chain(mux, logRequests(logger)))
}

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	if err := a.readiness.CheckReady(r.Context()); err != nil {
		a.logger.Warn("readiness check failed", "error", err)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
