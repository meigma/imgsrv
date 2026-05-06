package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

const (
	// blobCacheControl is the immutable-cache policy for verified CAS blobs.
	blobCacheControl = "public, max-age=31536000, immutable"

	// blobDefaultContentType is the fallback media type when trusted blob metadata has no media type.
	blobDefaultContentType = "application/octet-stream"
)

// errBlobServiceUnavailable signals that blob routes were called without a configured BlobService.
var errBlobServiceUnavailable = errors.New("blob service is not configured")

type selectedBlobRange struct {
	start  int64
	end    int64
	length int64
	open   objectstore.ByteRange
}

// getBlob handles GET and HEAD /v1/blobs/{digest} and serves trusted CAS blob bytes.
func (a *api) getBlob(w http.ResponseWriter, r *http.Request) {
	service, ok := a.blobService(w, r)
	if !ok {
		return
	}

	digest, err := uploads.ParseDigest(r.PathValue("digest"))
	if err != nil {
		writeBlobProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}

	blob, err := service.GetBlob(r.Context(), cas.GetBlobParams{Digest: digest})
	if err != nil {
		writeBlobLookupError(w, r, err)
		return
	}

	etag := blobETag(blob)
	modifiedAt := blob.VerifiedAt.UTC().Truncate(time.Second)

	if blobNotModified(r, etag, modifiedAt) {
		writeBlobHeaders(w, blob, etag, modifiedAt)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	rangeRequest, ok, err := selectBlobRange(r, etag, modifiedAt, blob.SizeBytes)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", blob.SizeBytes))
		writeBlobProblem(w, r, http.StatusRequestedRangeNotSatisfiable, err.Error())
		return
	}

	status := http.StatusOK
	contentLength := blob.SizeBytes
	if ok {
		status = http.StatusPartialContent
		contentLength = rangeRequest.length
	}

	var byteRange *objectstore.ByteRange
	if ok {
		byteRange = &rangeRequest.open
	}
	reader, err := openBlobReader(r.Context(), service, digest, byteRange)
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
		a.logger.Warn("stream blob response failed", "digest", digest, "error", err)
	}
}

// blobService returns the configured BlobService or writes a 503 response and reports false.
func (a *api) blobService(w http.ResponseWriter, r *http.Request) (BlobService, bool) {
	if a.blobs == nil {
		writeBlobProblem(w, r, http.StatusServiceUnavailable, errBlobServiceUnavailable.Error())
		return nil, false
	}

	return a.blobs, true
}

// writeBlobHeaders sets the stable caching and metadata headers for a blob response.
func writeBlobHeaders(w http.ResponseWriter, blob cas.Blob, etag string, modifiedAt time.Time) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", blobCacheControl)
	w.Header().Set("Content-Type", blobContentType(blob))
	w.Header().Set("ETag", etag)
	if !modifiedAt.IsZero() {
		w.Header().Set("Last-Modified", modifiedAt.Format(http.TimeFormat))
	}
}

// writeBlobSuccess writes the success headers and status for one blob response.
func writeBlobSuccess(
	w http.ResponseWriter,
	blob cas.Blob,
	etag string,
	modifiedAt time.Time,
	status int,
	contentLength int64,
	selected selectedBlobRange,
	partial bool,
) {
	writeBlobHeaders(w, blob, etag, modifiedAt)
	if partial {
		w.Header().Set("Content-Range", formatContentRange(selected.start, selected.end, blob.SizeBytes))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	w.WriteHeader(status)
}

// openBlobReader opens a trusted blob for one GET or HEAD request.
func openBlobReader(
	ctx context.Context,
	service BlobService,
	digest uploads.Digest,
	byteRange *objectstore.ByteRange,
) (objectstore.ObjectReader, error) {
	return service.OpenBlob(ctx, cas.OpenBlobParams{
		Digest: digest,
		Range:  byteRange,
	})
}

// writeBlobProblem writes a problem response for blob routes, suppressing the body on HEAD.
func writeBlobProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(problemResponse{
		Type:   defaultProblemType,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// writeBlobLookupError maps a trusted-blob metadata error to an HTTP response.
func writeBlobLookupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cas.ErrInvalid):
		writeBlobProblem(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, cas.ErrNotFound):
		writeBlobProblem(w, r, http.StatusNotFound, err.Error())
	default:
		writeBlobProblem(w, r, http.StatusInternalServerError, err.Error())
	}
}

// writeBlobReadError maps a blob-read error to an HTTP response after the blob metadata lookup succeeded.
func writeBlobReadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cas.ErrInvalid):
		writeBlobProblem(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, cas.ErrNotFound):
		writeBlobProblem(w, r, http.StatusNotFound, err.Error())
	default:
		writeBlobProblem(w, r, http.StatusInternalServerError, err.Error())
	}
}

// blobETag returns the strong ETag used for verified CAS blob responses.
func blobETag(blob cas.Blob) string {
	return `"` + blob.Digest.String() + `"`
}

// blobContentType returns the trusted blob media type or the default binary media type.
func blobContentType(blob cas.Blob) string {
	if blob.MediaType != nil && strings.TrimSpace(*blob.MediaType) != "" {
		return *blob.MediaType
	}

	return blobDefaultContentType
}

// blobNotModified reports whether request cache validators match the current blob representation.
func blobNotModified(r *http.Request, etag string, modifiedAt time.Time) bool {
	if method := r.Method; method != http.MethodGet && method != http.MethodHead {
		return false
	}

	if matchHeader(r.Header.Get("If-None-Match"), etag, true) {
		return true
	}
	if strings.TrimSpace(r.Header.Get("If-None-Match")) != "" {
		return false
	}

	if modifiedAt.IsZero() {
		return false
	}
	ifModifiedSince, err := http.ParseTime(r.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}

	return !modifiedAt.After(ifModifiedSince)
}

// selectBlobRange returns the selected byte range when the request asks for one and the
// current validators allow it.
func selectBlobRange(
	r *http.Request,
	etag string,
	modifiedAt time.Time,
	size int64,
) (selectedBlobRange, bool, error) {
	rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
	if rangeHeader == "" {
		return selectedBlobRange{}, false, nil
	}
	if !ifRangeAllows(r.Header.Get("If-Range"), etag, modifiedAt) {
		return selectedBlobRange{}, false, nil
	}

	return parseBlobRangeHeader(rangeHeader, size)
}

// ifRangeAllows reports whether an If-Range validator allows the request range to be honored.
func ifRangeAllows(header string, etag string, modifiedAt time.Time) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return true
	}
	if strings.HasPrefix(header, `"`) || strings.HasPrefix(header, "W/") {
		return matchHeader(header, etag, false)
	}
	if modifiedAt.IsZero() {
		return false
	}
	parsed, err := http.ParseTime(header)
	if err != nil {
		return false
	}

	return !modifiedAt.After(parsed)
}

// parseBlobRangeHeader parses the supported single-range forms for a blob response.
func parseBlobRangeHeader(header string, size int64) (selectedBlobRange, bool, error) {
	if !strings.HasPrefix(header, "bytes=") {
		return selectedBlobRange{}, false, errors.New("range unit must be bytes")
	}
	spec := strings.TrimSpace(strings.TrimPrefix(header, "bytes="))
	if spec == "" {
		return selectedBlobRange{}, false, errors.New("range must not be empty")
	}
	if strings.Contains(spec, ",") {
		return selectedBlobRange{}, false, errors.New("multiple ranges are not supported")
	}
	if size == 0 {
		return selectedBlobRange{}, false, errors.New("range is unsatisfiable for an empty blob")
	}

	if suffixLength, ok := strings.CutPrefix(spec, "-"); ok {
		length, err := strconv.ParseInt(suffixLength, 10, 64)
		if err != nil || length <= 0 {
			return selectedBlobRange{}, false, errors.New("suffix range must use a positive integer length")
		}
		if length > size {
			length = size
		}
		start := size - length
		end := size - 1
		return selectedBlobRange{
			start:  start,
			end:    end,
			length: length,
			open: objectstore.ByteRange{
				Start: start,
				End:   end,
			},
		}, true, nil
	}

	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return selectedBlobRange{}, false, errors.New("range must use start-end syntax")
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startText), 10, 64)
	if err != nil || start < 0 {
		return selectedBlobRange{}, false, errors.New("range start must be a non-negative integer")
	}
	if start >= size {
		return selectedBlobRange{}, false, errors.New("range start exceeds blob size")
	}

	endText = strings.TrimSpace(endText)
	if endText == "" {
		end := size - 1
		return selectedBlobRange{
			start:  start,
			end:    end,
			length: end - start + 1,
			open: objectstore.ByteRange{
				Start:     start,
				OpenEnded: true,
			},
		}, true, nil
	}

	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end < 0 {
		return selectedBlobRange{}, false, errors.New("range end must be a non-negative integer")
	}
	if end < start {
		return selectedBlobRange{}, false, errors.New("range end must be greater than or equal to range start")
	}
	if end >= size {
		end = size - 1
	}

	return selectedBlobRange{
		start:  start,
		end:    end,
		length: end - start + 1,
		open: objectstore.ByteRange{
			Start: start,
			End:   end,
		},
	}, true, nil
}

// formatContentRange formats a Content-Range header value for one selected byte range.
func formatContentRange(start int64, end int64, size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", start, end, size)
}

// matchHeader reports whether header matches etag, optionally allowing weak validators.
func matchHeader(header string, etag string, allowWeak bool) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		if candidate == etag {
			return true
		}
		if allowWeak && strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}

	return false
}
