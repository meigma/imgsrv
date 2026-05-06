package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// BlobsClient provides raw CAS blob API operations.
type BlobsClient interface {
	// HeadBlob returns metadata for a trusted CAS blob.
	HeadBlob(context.Context, Digest) (BlobMetadata, error)

	// OpenBlob opens a trusted CAS blob for reading.
	OpenBlob(context.Context, Digest, OpenBlobOptions) (BlobReadCloser, error)
}

// HTTPBlobsClient is the concrete HTTP implementation of BlobsClient.
type HTTPBlobsClient struct {
	// transport carries the HTTP configuration shared with the parent Client.
	transport *transport
}

var _ BlobsClient = (*HTTPBlobsClient)(nil)

// BlobMetadata describes the HTTP metadata for one blob response.
type BlobMetadata struct {
	// Digest identifies the trusted CAS blob.
	Digest Digest

	// SizeBytes is the total blob size.
	SizeBytes int64

	// ContentLength is the body size for this specific response.
	ContentLength int64

	// ContentType is the response media type.
	ContentType string

	// ETag is the strong HTTP entity tag for the blob.
	ETag string

	// CacheControl is the cache policy for the blob response.
	CacheControl string

	// LastModified is the HTTP Last-Modified header value.
	LastModified string

	// AcceptRanges is the HTTP Accept-Ranges header value.
	AcceptRanges string

	// ContentRange is the HTTP Content-Range header value for partial responses.
	ContentRange string
}

// BlobReadCloser returns streaming blob bytes plus the response metadata.
type BlobReadCloser struct {
	// Metadata describes the blob response headers.
	Metadata BlobMetadata

	// Body streams the blob bytes. Callers must close it.
	Body io.ReadCloser
}

// OpenBlobOptions configures one blob-read request.
type OpenBlobOptions struct {
	// Range optionally limits the returned bytes.
	Range *BlobRange
}

// BlobRange describes one supported single-range blob request.
type BlobRange struct {
	headerValue string
}

// BlobRangeSpan returns a range that reads bytes from start through end, inclusive.
func BlobRangeSpan(start int64, end int64) (BlobRange, error) {
	if start < 0 {
		return BlobRange{}, errors.New("blob range start must be non-negative")
	}
	if end < start {
		return BlobRange{}, errors.New("blob range end must be greater than or equal to start")
	}

	return BlobRange{headerValue: fmt.Sprintf("bytes=%d-%d", start, end)}, nil
}

// BlobRangeFrom returns a range that reads from start through the end of the blob.
func BlobRangeFrom(start int64) (BlobRange, error) {
	if start < 0 {
		return BlobRange{}, errors.New("blob range start must be non-negative")
	}

	return BlobRange{headerValue: fmt.Sprintf("bytes=%d-", start)}, nil
}

// BlobRangeSuffix returns a range that reads the last length bytes of the blob.
func BlobRangeSuffix(length int64) (BlobRange, error) {
	if length <= 0 {
		return BlobRange{}, errors.New("blob suffix length must be positive")
	}

	return BlobRange{headerValue: fmt.Sprintf("bytes=-%d", length)}, nil
}

// newHTTPBlobsClient binds the blob operation group to the shared transport.
func newHTTPBlobsClient(transport *transport) *HTTPBlobsClient {
	return &HTTPBlobsClient{transport: transport}
}

// HeadBlob returns metadata for a trusted CAS blob.
func (client *HTTPBlobsClient) HeadBlob(ctx context.Context, digest Digest) (BlobMetadata, error) {
	path := "/v1/blobs/" + url.PathEscape(digest.String())
	headers, _, err := blobHeaders(nil)
	if err != nil {
		return BlobMetadata{}, err
	}

	resp, err := client.transport.doResponse(ctx, http.MethodHead, path, nil, 0, headers, http.StatusOK)
	if err != nil {
		return BlobMetadata{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	return blobMetadataFromResponse(digest, resp)
}

// OpenBlob opens a trusted CAS blob for reading.
func (client *HTTPBlobsClient) OpenBlob(
	ctx context.Context,
	digest Digest,
	options OpenBlobOptions,
) (BlobReadCloser, error) {
	headers, wantStatus, err := blobHeaders(options.Range)
	if err != nil {
		return BlobReadCloser{}, err
	}

	path := "/v1/blobs/" + url.PathEscape(digest.String())
	resp, err := client.transport.doResponse(ctx, http.MethodGet, path, nil, 0, headers, wantStatus)
	if err != nil {
		return BlobReadCloser{}, err
	}

	metadata, err := blobMetadataFromResponse(digest, resp)
	if err != nil {
		_ = resp.Body.Close()
		return BlobReadCloser{}, err
	}

	return BlobReadCloser{
		Metadata: metadata,
		Body:     resp.Body,
	}, nil
}

// blobHeaders returns the headers and expected success status for one blob request.
func blobHeaders(blobRange *BlobRange) (http.Header, int, error) {
	headers := make(http.Header)
	headers.Set("Accept", "*/*")

	wantStatus := http.StatusOK
	if blobRange == nil {
		return headers, wantStatus, nil
	}
	if strings.TrimSpace(blobRange.headerValue) == "" {
		return nil, 0, errors.New("blob range must not be empty")
	}

	headers.Set("Range", blobRange.headerValue)
	return headers, http.StatusPartialContent, nil
}

// blobMetadataFromResponse reads blob metadata from a successful HTTP response.
func blobMetadataFromResponse(digest Digest, resp *http.Response) (BlobMetadata, error) {
	sizeBytes, err := totalBlobSize(resp)
	if err != nil {
		return BlobMetadata{}, err
	}

	contentLength := resp.ContentLength
	if contentLength < 0 {
		contentLength, err = parseInt64Header(resp.Header.Get("Content-Length"))
		if err != nil {
			return BlobMetadata{}, err
		}
	}

	return BlobMetadata{
		Digest:        digest,
		SizeBytes:     sizeBytes,
		ContentLength: contentLength,
		ContentType:   resp.Header.Get("Content-Type"),
		ETag:          resp.Header.Get("ETag"),
		CacheControl:  resp.Header.Get("Cache-Control"),
		LastModified:  resp.Header.Get("Last-Modified"),
		AcceptRanges:  resp.Header.Get("Accept-Ranges"),
		ContentRange:  resp.Header.Get("Content-Range"),
	}, nil
}

// totalBlobSize returns the total blob size from one successful blob response.
func totalBlobSize(resp *http.Response) (int64, error) {
	if resp.StatusCode == http.StatusPartialContent {
		return sizeFromContentRange(resp.Header.Get("Content-Range"))
	}

	return parseInt64Header(resp.Header.Get("Content-Length"))
}

// sizeFromContentRange parses the trailing total object size from one Content-Range header.
func sizeFromContentRange(contentRange string) (int64, error) {
	_, sizeText, ok := strings.Cut(contentRange, "/")
	if !ok {
		return 0, fmt.Errorf("parse Content-Range %q: missing total size", contentRange)
	}

	return parseInt64Header(sizeText)
}

// parseInt64Header parses one non-empty signed base-10 integer header value.
func parseInt64Header(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("expected integer response header is missing")
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse integer response header %q: %w", value, err)
	}

	return parsed, nil
}
