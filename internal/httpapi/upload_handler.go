package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/uploads"
)

// jsonControlBodyLimitBytes caps the size of JSON control-plane request bodies.
const jsonControlBodyLimitBytes = 1 << 20

// errUploadServiceUnavailable signals that upload routes were called without a configured UploadService.
var errUploadServiceUnavailable = errors.New("upload service is not configured")

// beginUploadRequest is the JSON body for POST /v1/uploads.
type beginUploadRequest struct {
	// ExpectedDigest is the sha256 digest the uploaded bytes must verify against.
	ExpectedDigest string `json:"expected_digest"`

	// ExpectedSizeBytes is the declared total size of the uploaded object.
	ExpectedSizeBytes int64 `json:"expected_size_bytes"`

	// MediaTypeHint optionally carries operator-provided content-type context.
	MediaTypeHint *string `json:"media_type_hint,omitempty"`

	// FilenameHint optionally carries operator-provided filename context.
	FilenameHint *string `json:"filename_hint,omitempty"`
}

// completeUploadRequest is the JSON body for POST /v1/uploads/{upload_id}/complete.
type completeUploadRequest struct {
	// Parts lists every uploaded part in S3-compatible multipart order.
	Parts []completeUploadPartRequest `json:"parts"`
}

// completeUploadPartRequest describes one part as the client observed it during upload.
type completeUploadPartRequest struct {
	// Number is the S3-compatible multipart part number.
	Number int `json:"number"`

	// ETag is the backing object-storage part ETag the server returned during PUT.
	ETag string `json:"etag"`

	// SizeBytes is the part size the client uploaded.
	SizeBytes int64 `json:"size_bytes"`
}

// uploadSessionResponse is the JSON wire shape of an upload session.
type uploadSessionResponse struct {
	// ID is the stable upload session identity.
	ID string `json:"id"`

	// ExpectedDigest is the sha256 digest the uploaded bytes must verify against.
	ExpectedDigest string `json:"expected_digest"`

	// ExpectedSizeBytes is the declared total size of the uploaded object.
	ExpectedSizeBytes int64 `json:"expected_size_bytes"`

	// State is the durable upload lifecycle state.
	State uploads.SessionState `json:"state"`

	// ExpiresAt is the RFC 3339 timestamp at which unfinished upload state may be cleaned up.
	ExpiresAt string `json:"expires_at"`

	// MediaTypeHint echoes the operator-provided content-type context when supplied.
	MediaTypeHint *string `json:"media_type_hint,omitempty"`

	// FilenameHint echoes the operator-provided filename context when supplied.
	FilenameHint *string `json:"filename_hint,omitempty"`
}

// uploadPartResponse is the JSON wire shape returned after a successful part PUT.
type uploadPartResponse struct {
	// UploadID identifies the parent upload session.
	UploadID string `json:"upload_id"`

	// PartNumber is the S3-compatible multipart part number that was accepted.
	PartNumber int `json:"part_number"`

	// ETag is the backing object-storage part ETag for the accepted bytes.
	ETag string `json:"etag"`

	// SizeBytes is the accepted part size.
	SizeBytes int64 `json:"size_bytes"`
}

// beginUpload handles POST /v1/uploads and starts a new upload session.
func (a *api) beginUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := a.uploadService(w)
	if !ok {
		return
	}

	var request beginUploadRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	digest, err := uploads.ParseDigest(request.ExpectedDigest)
	if err != nil {
		writeUploadError(w, err)
		return
	}

	result, err := service.BeginUpload(r.Context(), uploads.BeginUploadParams{
		ExpectedDigest:    digest,
		ExpectedSizeBytes: request.ExpectedSizeBytes,
		MediaTypeHint:     request.MediaTypeHint,
		FilenameHint:      request.FilenameHint,
		ExpiresAt:         a.now().Add(a.uploadTTL),
	})
	if err != nil {
		writeUploadError(w, err)
		return
	}

	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	a.logger.InfoContext(
		r.Context(),
		"upload session started",
		"operation",
		"upload.begin",
		"request_id",
		RequestIDFromContext(r.Context()),
		"upload_id",
		result.Session.ID.String(),
		"expected_digest",
		result.Session.ExpectedDigest.String(),
		"expected_size_bytes",
		result.Session.ExpectedSizeBytes,
		"state",
		string(result.Session.State),
		"created",
		result.Created,
	)

	writeJSON(w, status, newUploadSessionResponse(result.Session))
}

// putUploadPart handles PUT /v1/uploads/{upload_id}/parts/{part_number} and stores or replaces one part.
func (a *api) putUploadPart(w http.ResponseWriter, r *http.Request) {
	service, ok := a.uploadService(w)
	if !ok {
		return
	}
	uploadID, partNumber, ok := parseUploadPartPath(w, r)
	if !ok {
		return
	}
	if r.ContentLength < 0 {
		writeProblem(w, http.StatusBadRequest, "content length is required")
		return
	}

	part, err := service.PutUploadPart(r.Context(), uploads.PutUploadPartParams{
		UploadID:   uploadID,
		PartNumber: partNumber,
		Body:       r.Body,
		SizeBytes:  r.ContentLength,
	})
	if err != nil {
		writeUploadError(w, err)
		return
	}
	a.logger.DebugContext(
		r.Context(),
		"upload part stored",
		"operation",
		"upload.put_part",
		"request_id",
		RequestIDFromContext(r.Context()),
		"upload_id",
		part.UploadID.String(),
		"part_number",
		part.PartNumber,
		"size_bytes",
		part.SizeBytes,
	)

	writeJSON(w, http.StatusOK, uploadPartResponse{
		UploadID:   part.UploadID.String(),
		PartNumber: part.PartNumber,
		ETag:       part.ETag,
		SizeBytes:  part.SizeBytes,
	})
}

// getUpload handles GET /v1/uploads/{upload_id} and returns current durable upload state.
func (a *api) getUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := a.uploadService(w)
	if !ok {
		return
	}
	uploadID, ok := parseUploadIDPath(w, r)
	if !ok {
		return
	}

	session, err := service.GetUpload(r.Context(), uploads.GetUploadParams{UploadID: uploadID})
	if err != nil {
		writeUploadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newUploadSessionResponse(session))
}

// completeUpload handles POST /v1/uploads/{upload_id}/complete and finalizes a staged multipart upload.
func (a *api) completeUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := a.uploadService(w)
	if !ok {
		return
	}
	uploadID, ok := parseUploadIDPath(w, r)
	if !ok {
		return
	}

	var request completeUploadRequest
	if err := decodeControlJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}

	parts := make([]uploads.CompleteUploadPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		parts = append(parts, uploads.CompleteUploadPart{
			Number:    part.Number,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		})
	}

	session, err := service.CompleteUpload(r.Context(), uploads.CompleteUploadParams{
		UploadID: uploadID,
		Parts:    parts,
	})
	if err != nil {
		writeUploadError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"upload completed",
		"operation",
		"upload.complete",
		"request_id",
		RequestIDFromContext(r.Context()),
		"upload_id",
		session.ID.String(),
		"expected_digest",
		session.ExpectedDigest.String(),
		"expected_size_bytes",
		session.ExpectedSizeBytes,
		"state",
		string(session.State),
		"part_count",
		len(parts),
	)

	writeJSON(w, http.StatusOK, newUploadSessionResponse(session))
}

// abortUpload handles POST /v1/uploads/{upload_id}/abort and aborts a mutable upload session.
func (a *api) abortUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := a.uploadService(w)
	if !ok {
		return
	}
	uploadID, ok := parseUploadIDPath(w, r)
	if !ok {
		return
	}

	session, err := service.AbortUpload(r.Context(), uploads.AbortUploadParams{UploadID: uploadID})
	if err != nil {
		writeUploadError(w, err)
		return
	}
	a.logger.InfoContext(
		r.Context(),
		"upload aborted",
		"operation",
		"upload.abort",
		"request_id",
		RequestIDFromContext(r.Context()),
		"upload_id",
		session.ID.String(),
		"state",
		string(session.State),
	)

	writeJSON(w, http.StatusOK, newUploadSessionResponse(session))
}

// uploadService returns the configured UploadService or writes a 503 problem and reports false.
func (a *api) uploadService(w http.ResponseWriter) (UploadService, bool) {
	if a.uploads == nil {
		writeProblem(w, http.StatusServiceUnavailable, errUploadServiceUnavailable.Error())
		return nil, false
	}

	return a.uploads, true
}

// parseUploadPartPath extracts and validates the upload ID and part number path values.
//
// On failure it writes a problem response and returns false; callers should not write further.
func parseUploadPartPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, int, bool) {
	uploadID, ok := parseUploadIDPath(w, r)
	if !ok {
		return uuid.Nil, 0, false
	}

	partNumber, err := strconv.Atoi(r.PathValue("part_number"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "part number must be an integer")
		return uuid.Nil, 0, false
	}
	if err := uploads.ValidatePartNumber(partNumber); err != nil {
		writeUploadError(w, err)
		return uuid.Nil, 0, false
	}

	return uploadID, partNumber, true
}

// parseUploadIDPath extracts and validates the upload ID path value.
//
// On failure it writes a problem response and returns false; callers should not write further.
func parseUploadIDPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	uploadID, err := uuid.Parse(r.PathValue("upload_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "upload id must be a UUID")
		return uuid.Nil, false
	}

	return uploadID, true
}

// decodeControlJSON strictly decodes a single JSON value into dst.
//
// The request body is bounded by jsonControlBodyLimitBytes, unknown fields are
// rejected, and bodies containing more than one JSON value are rejected.
func decodeControlJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, jsonControlBodyLimitBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON request body: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid JSON request body: multiple JSON values")
	}

	return nil
}

// writeJSON writes value as application/json with the provided status code.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// newUploadSessionResponse projects a durable upload session onto its JSON wire shape.
func newUploadSessionResponse(session uploads.Session) uploadSessionResponse {
	return uploadSessionResponse{
		ID:                session.ID.String(),
		ExpectedDigest:    session.ExpectedDigest.String(),
		ExpectedSizeBytes: session.ExpectedSizeBytes,
		State:             session.State,
		ExpiresAt:         session.ExpiresAt.Format(time.RFC3339Nano),
		MediaTypeHint:     session.MediaTypeHint,
		FilenameHint:      session.FilenameHint,
	}
}
