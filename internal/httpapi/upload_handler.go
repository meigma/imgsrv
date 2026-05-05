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

const jsonControlBodyLimitBytes = 1 << 20

var errUploadServiceUnavailable = errors.New("upload service is not configured")

type beginUploadRequest struct {
	ExpectedDigest    string  `json:"expected_digest"`
	ExpectedSizeBytes int64   `json:"expected_size_bytes"`
	MediaTypeHint     *string `json:"media_type_hint,omitempty"`
	FilenameHint      *string `json:"filename_hint,omitempty"`
}

type completeUploadRequest struct {
	Parts []completeUploadPartRequest `json:"parts"`
}

type completeUploadPartRequest struct {
	Number    int    `json:"number"`
	ETag      string `json:"etag"`
	SizeBytes int64  `json:"size_bytes"`
}

type uploadSessionResponse struct {
	ID                string               `json:"id"`
	ExpectedDigest    string               `json:"expected_digest"`
	ExpectedSizeBytes int64                `json:"expected_size_bytes"`
	State             uploads.SessionState `json:"state"`
	ExpiresAt         string               `json:"expires_at"`
	MediaTypeHint     *string              `json:"media_type_hint,omitempty"`
	FilenameHint      *string              `json:"filename_hint,omitempty"`
}

type uploadPartResponse struct {
	UploadID   string `json:"upload_id"`
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	SizeBytes  int64  `json:"size_bytes"`
}

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

	session, err := service.BeginUpload(r.Context(), uploads.BeginUploadParams{
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

	writeJSON(w, http.StatusCreated, newUploadSessionResponse(session))
}

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

	writeJSON(w, http.StatusOK, uploadPartResponse{
		UploadID:   part.UploadID.String(),
		PartNumber: part.PartNumber,
		ETag:       part.ETag,
		SizeBytes:  part.SizeBytes,
	})
}

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

	writeJSON(w, http.StatusOK, newUploadSessionResponse(session))
}

func (a *api) uploadService(w http.ResponseWriter) (UploadService, bool) {
	if a.uploads == nil {
		writeProblem(w, http.StatusServiceUnavailable, errUploadServiceUnavailable.Error())
		return nil, false
	}

	return a.uploads, true
}

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

func parseUploadIDPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	uploadID, err := uuid.Parse(r.PathValue("upload_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "upload id must be a UUID")
		return uuid.Nil, false
	}

	return uploadID, true
}

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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

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
