package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// UploadsClient provides upload API operations.
type UploadsClient interface {
	// BeginUpload starts a new upload session.
	BeginUpload(context.Context, BeginUploadRequest) (UploadSession, error)

	// PutUploadPart stores or replaces one upload part.
	PutUploadPart(context.Context, string, int, io.Reader, int64) (UploadPart, error)

	// CompleteUpload completes a staged multipart upload.
	CompleteUpload(context.Context, string, CompleteUploadRequest) (UploadSession, error)

	// AbortUpload aborts a mutable upload session.
	AbortUpload(context.Context, string) (UploadSession, error)

	// GetUpload returns current durable upload state.
	GetUpload(context.Context, string) (UploadSession, error)
}

// HTTPUploadsClient is the concrete HTTP implementation of UploadsClient.
type HTTPUploadsClient struct {
	transport *transport
}

var _ UploadsClient = (*HTTPUploadsClient)(nil)

// BeginUploadRequest starts a content upload for an expected digest.
type BeginUploadRequest struct {
	// ExpectedDigest is the digest the uploaded bytes must verify against.
	ExpectedDigest Digest `json:"expected_digest"`

	// ExpectedSizeBytes is the declared final object size.
	ExpectedSizeBytes int64 `json:"expected_size_bytes"`

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string `json:"media_type_hint,omitempty"`

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string `json:"filename_hint,omitempty"`
}

// CompleteUploadRequest completes a content upload from accepted parts.
type CompleteUploadRequest struct {
	// Parts are the accepted upload parts to commit.
	Parts []CompleteUploadPart `json:"parts"`
}

// CompleteUploadPart identifies one accepted upload part.
type CompleteUploadPart struct {
	// Number is the S3-compatible multipart part number.
	Number int `json:"number"`

	// ETag is the part ETag returned by PutUploadPart.
	ETag string `json:"etag"`

	// SizeBytes is the accepted part size.
	SizeBytes int64 `json:"size_bytes"`
}

// UploadSession describes current durable upload state.
type UploadSession struct {
	// ID is the stable upload session identity.
	ID UploadID `json:"id"`

	// ExpectedDigest is the digest the uploaded bytes must verify against.
	ExpectedDigest Digest `json:"expected_digest"`

	// ExpectedSizeBytes is the declared final object size.
	ExpectedSizeBytes int64 `json:"expected_size_bytes"`

	// State is the durable upload lifecycle state.
	State UploadState `json:"state"`

	// ExpiresAt is the RFC3339 timestamp when unfinished upload state is eligible for cleanup.
	ExpiresAt string `json:"expires_at"`

	// MediaTypeHint is optional operator-provided content-type context.
	MediaTypeHint *string `json:"media_type_hint,omitempty"`

	// FilenameHint is optional operator-provided filename context.
	FilenameHint *string `json:"filename_hint,omitempty"`
}

// UploadPart describes one accepted upload part.
type UploadPart struct {
	// UploadID identifies the parent upload session.
	UploadID UploadID `json:"upload_id"`

	// PartNumber is the S3-compatible multipart part number.
	PartNumber int `json:"part_number"`

	// ETag is the accepted part ETag.
	ETag string `json:"etag"`

	// SizeBytes is the accepted part size.
	SizeBytes int64 `json:"size_bytes"`
}

func newHTTPUploadsClient(transport *transport) *HTTPUploadsClient {
	return &HTTPUploadsClient{transport: transport}
}

// BeginUpload starts a new upload session.
func (client *HTTPUploadsClient) BeginUpload(ctx context.Context, request BeginUploadRequest) (UploadSession, error) {
	var session UploadSession
	err := client.transport.doJSON(ctx, http.MethodPost, "/v1/uploads", request, nil, http.StatusCreated, &session)

	return session, err
}

// PutUploadPart stores or replaces one upload part.
func (client *HTTPUploadsClient) PutUploadPart(
	ctx context.Context,
	uploadID string,
	partNumber int,
	body io.Reader,
	sizeBytes int64,
) (UploadPart, error) {
	var part UploadPart
	path := "/v1/uploads/" + url.PathEscape(uploadID) + "/parts/" + strconv.Itoa(partNumber)
	headers := http.Header{"Content-Type": []string{"application/octet-stream"}}
	err := client.transport.do(ctx, http.MethodPut, path, body, sizeBytes, headers, http.StatusOK, &part)

	return part, err
}

// CompleteUpload completes a staged multipart upload.
func (client *HTTPUploadsClient) CompleteUpload(
	ctx context.Context,
	uploadID string,
	request CompleteUploadRequest,
) (UploadSession, error) {
	var session UploadSession
	path := "/v1/uploads/" + url.PathEscape(uploadID) + "/complete"
	err := client.transport.doJSON(ctx, http.MethodPost, path, request, nil, http.StatusOK, &session)

	return session, err
}

// AbortUpload aborts a mutable upload session.
func (client *HTTPUploadsClient) AbortUpload(ctx context.Context, uploadID string) (UploadSession, error) {
	var session UploadSession
	path := "/v1/uploads/" + url.PathEscape(uploadID) + "/abort"
	err := client.transport.do(ctx, http.MethodPost, path, nil, 0, nil, http.StatusOK, &session)

	return session, err
}

// GetUpload returns current durable upload state.
func (client *HTTPUploadsClient) GetUpload(ctx context.Context, uploadID string) (UploadSession, error) {
	var session UploadSession
	path := "/v1/uploads/" + url.PathEscape(uploadID)
	err := client.transport.do(ctx, http.MethodGet, path, nil, 0, nil, http.StatusOK, &session)

	return session, err
}
