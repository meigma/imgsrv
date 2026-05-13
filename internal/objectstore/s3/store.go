// Package s3 implements objectstore.Store with an S3-compatible backend.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	safelog "github.com/meigma/imgsrv/internal/logging"
	"github.com/meigma/imgsrv/internal/objectstore"
)

const (
	// noSuchBucketCode is the S3 error code returned when the bucket does not exist.
	noSuchBucketCode = "NoSuchBucket"
	// noSuchKeyCode is the S3 error code returned when the object key does not exist.
	noSuchKeyCode = "NoSuchKey"
	// noSuchUploadCode is the S3 error code returned when the multipart upload does not exist.
	noSuchUploadCode = "NoSuchUpload"

	// preconditionFailedCode is the S3 error code returned when a conditional precondition fails.
	preconditionFailedCode = "PreconditionFailed"
	// conditionalRequestConflictCode is the S3 error code returned when a conditional request hits a concurrent conflict.
	conditionalRequestConflictCode = "ConditionalRequestConflict"

	// maxSingleCopySizeBytes is the largest object size that S3 supports through a single CopyObject call.
	maxSingleCopySizeBytes = 5 * 1000 * 1000 * 1000
	// maxCopyPartSizeBytes is the largest byte range that S3 supports per UploadPartCopy request.
	maxCopyPartSizeBytes = 5 * 1024 * 1024 * 1024
	// maxCopyPartCount is the highest part number S3 supports during a multipart copy.
	maxCopyPartCount = 10000
)

// Config configures an S3-compatible object store.
type Config struct {
	// Endpoint is the S3-compatible API endpoint without a URL scheme.
	Endpoint string

	// Bucket is the bucket that stores all object keys.
	Bucket string

	// AccessKeyID is the S3 access key ID.
	AccessKeyID string

	// SecretAccessKey is the S3 secret access key.
	SecretAccessKey string

	// SessionToken is the optional temporary credential session token.
	SessionToken string

	// Region is the optional S3 region.
	Region string

	// UseTLS enables HTTPS when connecting to Endpoint.
	UseTLS bool

	// PathStyle forces path-style bucket addressing when true.
	PathStyle bool

	// Logger receives sanitized S3 adapter logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Validate checks that config can construct an S3 object store.
func (config Config) Validate() error {
	if err := objectstore.ValidateRequiredText("endpoint", config.Endpoint); err != nil {
		return err
	}
	if strings.Contains(config.Endpoint, "://") {
		return fmt.Errorf("%w: endpoint must not include a URL scheme", objectstore.ErrInvalid)
	}
	if err := objectstore.ValidateRequiredText("bucket", config.Bucket); err != nil {
		return err
	}
	if err := objectstore.ValidateRequiredText("access key id", config.AccessKeyID); err != nil {
		return err
	}
	return objectstore.ValidateRequiredText("secret access key", config.SecretAccessKey)
}

// Store adapts an S3-compatible bucket to objectstore.Store.
type Store struct {
	// core is the underlying minio low-level client used for S3 operations.
	core *minio.Core
	// bucket is the S3 bucket that backs every key handled by this store.
	bucket string
	// logger receives sanitized S3 adapter logs.
	logger *slog.Logger
}

// New constructs an S3 object store from config.
func New(config Config) (*Store, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	bucketLookup := minio.BucketLookupAuto
	if config.PathStyle {
		bucketLookup = minio.BucketLookupPath
	}

	core, err := minio.NewCore(config.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			config.AccessKeyID,
			config.SecretAccessKey,
			config.SessionToken,
		),
		Secure:       config.UseTLS,
		Region:       config.Region,
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, mapError(err)
	}

	logger.Info(
		"s3 object store configured",
		"operation",
		"s3.open",
		"endpoint",
		config.Endpoint,
		"bucket",
		config.Bucket,
		"region",
		config.Region,
		"use_tls",
		config.UseTLS,
		"path_style",
		config.PathStyle,
	)

	return &Store{
		core:   core,
		bucket: config.Bucket,
		logger: logger,
	}, nil
}

// CreateMultipartUpload starts a multipart object write.
func (store *Store) CreateMultipartUpload(
	ctx context.Context,
	params objectstore.CreateMultipartUploadParams,
) (objectstore.MultipartUpload, error) {
	if err := params.Validate(); err != nil {
		return objectstore.MultipartUpload{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.MultipartUpload{}, err
	}

	uploadID, err := core.NewMultipartUpload(ctx, bucket, params.Key, minio.PutObjectOptions{})
	if err != nil {
		return objectstore.MultipartUpload{}, mapError(err)
	}

	return objectstore.MultipartUpload{Key: params.Key, UploadID: uploadID}, nil
}

// PutPart uploads or replaces one multipart object part.
func (store *Store) PutPart(
	ctx context.Context,
	params objectstore.PutPartParams,
) (objectstore.Part, error) {
	if err := params.Validate(); err != nil {
		return objectstore.Part{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.Part{}, err
	}

	part, err := core.PutObjectPart(
		ctx,
		bucket,
		params.Key,
		params.UploadID,
		params.PartNumber,
		params.Body,
		params.SizeBytes,
		minio.PutObjectPartOptions{},
	)
	if err != nil {
		return objectstore.Part{}, mapError(err)
	}

	return objectstore.Part{
		Number:    part.PartNumber,
		ETag:      part.ETag,
		SizeBytes: part.Size,
	}, nil
}

// CompleteMultipartUpload commits an in-progress multipart object write.
func (store *Store) CompleteMultipartUpload(
	ctx context.Context,
	params objectstore.CompleteMultipartUploadParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	parts, err := objectstore.NormalizeCompleteParts(params.Parts)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	uploadInfo, err := core.CompleteMultipartUpload(
		ctx,
		bucket,
		params.Key,
		params.UploadID,
		completeParts(parts),
		minio.PutObjectOptions{},
	)
	if err != nil {
		return objectstore.ObjectInfo{}, mapError(err)
	}

	return uploadObjectInfo(uploadInfo, params.Key, completePartsSize(parts)), nil
}

// AbortMultipartUpload aborts an in-progress multipart object write.
func (store *Store) AbortMultipartUpload(
	ctx context.Context,
	params objectstore.AbortMultipartUploadParams,
) error {
	if err := params.Validate(); err != nil {
		return err
	}

	core, bucket, err := store.client()
	if err != nil {
		return err
	}

	if err := core.AbortMultipartUpload(ctx, bucket, params.Key, params.UploadID); err != nil {
		return mapError(err)
	}

	return nil
}

// OpenObject opens a stored object for reading.
func (store *Store) OpenObject(
	ctx context.Context,
	params objectstore.OpenObjectParams,
) (objectstore.ObjectReader, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectReader{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.ObjectReader{}, err
	}

	getOptions, err := getObjectOptions(params)
	if err != nil {
		return objectstore.ObjectReader{}, err
	}

	body, info, headers, err := core.GetObject(ctx, bucket, params.Key, getOptions)
	if err != nil {
		return objectstore.ObjectReader{}, mapError(err)
	}

	objectInfo := objectInfo(info, params.Key)
	if hasEffectiveRange(params.Range) {
		objectSize, err := objectSizeFromContentRange(headers.Get("Content-Range"))
		if err != nil {
			return objectstore.ObjectReader{}, closeAfterError(body, err)
		}
		objectInfo.SizeBytes = objectSize
	}

	return objectstore.ObjectReader{
		Info: objectInfo,
		Body: body,
	}, nil
}

// StatObject looks up stored object metadata.
func (store *Store) StatObject(
	ctx context.Context,
	params objectstore.StatObjectParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	info, err := core.StatObject(ctx, bucket, params.Key, minio.StatObjectOptions{})
	if err != nil {
		return objectstore.ObjectInfo{}, mapError(err)
	}

	return objectInfo(info, params.Key), nil
}

// CopyObject copies a stored object to another key.
func (store *Store) CopyObject(
	ctx context.Context,
	params objectstore.CopyObjectParams,
) (objectstore.ObjectInfo, error) {
	if err := params.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}

	core, bucket, err := store.client()
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	sourceInfo, err := core.StatObject(ctx, bucket, params.SourceKey, minio.StatObjectOptions{})
	if err != nil {
		return objectstore.ObjectInfo{}, mapError(err)
	}
	if requiresMultipartCopy(sourceInfo.Size) {
		store.logger.DebugContext(
			ctx,
			"s3 multipart copy selected",
			"operation",
			"s3.copy_object",
			"source_key",
			params.SourceKey,
			"destination_key",
			params.DestinationKey,
			"size_bytes",
			sourceInfo.Size,
		)
		return store.copyObjectMultipart(ctx, params, sourceInfo)
	}

	uploadInfo, err := core.CopyObject(
		ctx,
		bucket,
		params.SourceKey,
		bucket,
		params.DestinationKey,
		copyHeaders(params, sourceInfo),
		minio.CopySrcOptions{},
		minio.PutObjectOptions{},
	)
	if err != nil {
		return objectstore.ObjectInfo{}, mapSingleCopyError(err)
	}

	return copyObjectInfo(uploadInfo, params.DestinationKey, sourceInfo.Size)
}

// DeleteObject deletes a stored object.
func (store *Store) DeleteObject(ctx context.Context, params objectstore.DeleteObjectParams) error {
	if err := params.Validate(); err != nil {
		return err
	}

	core, bucket, err := store.client()
	if err != nil {
		return err
	}

	if err := core.RemoveObject(ctx, bucket, params.Key, minio.RemoveObjectOptions{}); err != nil {
		return mapError(err)
	}

	return nil
}

// copyObjectMultipart copies a stored object using S3 multipart copy when the object exceeds
// the single-copy size limit. It aborts the in-progress multipart upload if completion fails.
func (store *Store) copyObjectMultipart(
	ctx context.Context,
	params objectstore.CopyObjectParams,
	sourceInfo minio.ObjectInfo,
) (objectstore.ObjectInfo, error) {
	core, bucket, err := store.client()
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	parts, err := multipartCopyParts(sourceInfo.Size)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}

	uploadID, err := core.NewMultipartUpload(ctx, bucket, params.DestinationKey, minio.PutObjectOptions{})
	if err != nil {
		return objectstore.ObjectInfo{}, mapError(err)
	}
	store.logger.DebugContext(
		ctx,
		"s3 multipart copy started",
		"operation",
		"s3.copy_object_multipart",
		"source_key",
		params.SourceKey,
		"destination_key",
		params.DestinationKey,
		"size_bytes",
		sourceInfo.Size,
		"part_count",
		len(parts),
	)

	completed := false
	defer func() {
		if completed {
			return
		}
		_ = core.AbortMultipartUpload(context.WithoutCancel(ctx), bucket, params.DestinationKey, uploadID)
	}()

	completeParts := make([]minio.CompletePart, 0, len(parts))
	matchSource := sourceMatchHeaders(params, sourceInfo)
	for _, part := range parts {
		completePart, copyErr := core.CopyObjectPart(
			ctx,
			bucket,
			params.SourceKey,
			bucket,
			params.DestinationKey,
			uploadID,
			part.number,
			part.start,
			part.size,
			matchSource,
		)
		if copyErr != nil {
			return objectstore.ObjectInfo{}, mapSingleCopyError(copyErr)
		}
		if partErr := validateCopiedPart(completePart); partErr != nil {
			return objectstore.ObjectInfo{}, partErr
		}
		completeParts = append(completeParts, completePart)
	}

	uploadInfo, err := core.CompleteMultipartUpload(
		ctx,
		bucket,
		params.DestinationKey,
		uploadID,
		completeParts,
		completeMultipartUploadOptions(params),
	)
	if err != nil {
		return objectstore.ObjectInfo{}, mapCopyError(err, params)
	}

	completed = true
	store.logger.DebugContext(
		ctx,
		"s3 multipart copy completed",
		"operation",
		"s3.copy_object_multipart",
		"source_key",
		params.SourceKey,
		"destination_key",
		params.DestinationKey,
		"size_bytes",
		sourceInfo.Size,
		"part_count",
		len(parts),
	)

	return uploadObjectInfo(uploadInfo, params.DestinationKey, sourceInfo.Size), nil
}

// client returns the underlying minio core client and bucket, or an error when the store
// has not been constructed through New.
func (store *Store) client() (*minio.Core, string, error) {
	if store == nil || store.core == nil || store.bucket == "" {
		return nil, "", errors.New("s3 objectstore is not open")
	}

	return store.core, store.bucket, nil
}

// multipartCopyPart describes one byte range to copy as a single UploadPartCopy request.
type multipartCopyPart struct {
	number int
	start  int64
	size   int64
}

// multipartCopyParts splits an object size into S3 multipart copy ranges that respect S3's
// per-part size limit and total part-count cap.
func multipartCopyParts(size int64) ([]multipartCopyPart, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: object size must be non-negative", objectstore.ErrInvalid)
	}
	if size > maxCopyPartSizeBytes*maxCopyPartCount {
		return nil, fmt.Errorf("%w: object is too large for multipart copy", objectstore.ErrInvalid)
	}
	if size == 0 {
		return nil, fmt.Errorf("%w: multipart copy requires a non-empty object", objectstore.ErrInvalid)
	}

	partCount := (size + maxCopyPartSizeBytes - 1) / maxCopyPartSizeBytes
	parts := make([]multipartCopyPart, 0, int(partCount))
	for start := int64(0); start < size; start += maxCopyPartSizeBytes {
		partSize := min(maxCopyPartSizeBytes, size-start)
		parts = append(parts, multipartCopyPart{
			number: len(parts) + 1,
			start:  start,
			size:   partSize,
		})
	}

	return parts, nil
}

// requiresMultipartCopy reports whether an object is too large for a single S3 CopyObject call.
func requiresMultipartCopy(size int64) bool {
	return size > maxSingleCopySizeBytes
}

// completePartsSize sums the byte sizes reported by every multipart completion part.
func completePartsSize(parts []objectstore.CompletePart) int64 {
	var size int64
	for _, part := range parts {
		size += part.SizeBytes
	}

	return size
}

// getObjectOptions builds the minio GetObjectOptions, including the optional byte-range header,
// for an OpenObject request.
func getObjectOptions(params objectstore.OpenObjectParams) (minio.GetObjectOptions, error) {
	options := minio.GetObjectOptions{}
	if params.Range == nil {
		return options, nil
	}
	if params.Range.OpenEnded && params.Range.Start == 0 {
		return options, nil
	}
	if params.Range.OpenEnded {
		return options, options.SetRange(params.Range.Start, 0)
	}

	return options, options.SetRange(params.Range.Start, params.Range.End)
}

// hasEffectiveRange reports whether byteRange would produce an actual S3 range request
// rather than a full-object read.
func hasEffectiveRange(byteRange *objectstore.ByteRange) bool {
	return byteRange != nil && (!byteRange.OpenEnded || byteRange.Start != 0)
}

// objectSizeFromContentRange parses the total object size from an S3 Content-Range header value.
func objectSizeFromContentRange(contentRange string) (int64, error) {
	_, sizeText, ok := strings.Cut(contentRange, "/")
	if !ok {
		return 0, errors.New("s3 ranged response is missing content-range object size")
	}

	sizeText = strings.TrimSpace(sizeText)
	if sizeText == "" || sizeText == "*" {
		return 0, errors.New("s3 ranged response is missing content-range object size")
	}

	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("s3 ranged response has invalid content-range object size %q", sizeText)
	}

	return size, nil
}

// closeAfterError closes closer and joins any close error with the original error.
func closeAfterError(closer io.Closer, err error) error {
	if closeErr := closer.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}

	return err
}

// copyHeaders returns the S3 conditional headers required for a single CopyObject call,
// or nil when no conditional headers apply.
func copyHeaders(params objectstore.CopyObjectParams, sourceInfo minio.ObjectInfo) map[string]string {
	headers := map[string]string{}
	if sourceETag := sourceMatchETag(params, sourceInfo); sourceETag != "" {
		headers["x-amz-copy-source-if-match"] = sourceETag
	}
	if params.IfDestinationAbsent {
		headers["If-None-Match"] = "*"
	}
	if len(headers) == 0 {
		return nil
	}

	return headers
}

// sourceMatchHeaders returns the x-amz-copy-source-if-match header used during multipart copy
// to keep every UploadPartCopy bound to the same source ETag, or nil when no source ETag is known.
func sourceMatchHeaders(params objectstore.CopyObjectParams, sourceInfo minio.ObjectInfo) map[string]string {
	sourceETag := sourceMatchETag(params, sourceInfo)
	if sourceETag == "" {
		return nil
	}

	return map[string]string{"x-amz-copy-source-if-match": sourceETag}
}

// sourceMatchETag picks the source ETag to enforce during a copy, preferring the caller-supplied
// IfSourceETag and falling back to the freshly observed source object ETag.
func sourceMatchETag(params objectstore.CopyObjectParams, sourceInfo minio.ObjectInfo) string {
	if sourceETag := strings.TrimSpace(params.IfSourceETag); sourceETag != "" {
		return sourceETag
	}

	return strings.TrimSpace(sourceInfo.ETag)
}

// mapSingleCopyError translates S3 conditional-copy precondition failures to objectstore.ErrConflict
// and otherwise defers to mapError.
func mapSingleCopyError(err error) error {
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case preconditionFailedCode, conditionalRequestConflictCode:
		return fmt.Errorf("%w: %w", objectstore.ErrConflict, err)
	default:
		return mapError(err)
	}
}

// completeMultipartUploadOptions returns the multipart-completion options that enforce
// destination-absent semantics when the caller requested IfDestinationAbsent.
func completeMultipartUploadOptions(params objectstore.CopyObjectParams) minio.PutObjectOptions {
	options := minio.PutObjectOptions{}
	if params.IfDestinationAbsent {
		options.SetMatchETagExcept("*")
	}

	return options
}

// completeParts converts objectstore complete parts into the minio multipart completion payload.
func completeParts(parts []objectstore.CompletePart) []minio.CompletePart {
	converted := make([]minio.CompletePart, 0, len(parts))
	for _, part := range parts {
		converted = append(converted, minio.CompletePart{
			PartNumber: part.Number,
			ETag:       part.ETag,
		})
	}

	return converted
}

// objectInfo converts a minio object info value into the port-level ObjectInfo, falling back
// to fallbackKey when the response key is empty.
func objectInfo(info minio.ObjectInfo, fallbackKey string) objectstore.ObjectInfo {
	return objectstore.ObjectInfo{
		Key:       firstNonEmpty(info.Key, fallbackKey),
		SizeBytes: info.Size,
		ETag:      info.ETag,
	}
}

// copyObjectInfo converts a minio CopyObject response into the port-level ObjectInfo and
// fails when S3 omitted the destination ETag.
func copyObjectInfo(info minio.ObjectInfo, fallbackKey string, sizeBytes int64) (objectstore.ObjectInfo, error) {
	if strings.TrimSpace(info.ETag) == "" {
		return objectstore.ObjectInfo{}, errors.New("s3 copy object response missing etag")
	}

	copiedInfo := objectInfo(info, fallbackKey)
	copiedInfo.SizeBytes = sizeBytes
	return copiedInfo, nil
}

// validateCopiedPart returns an error when an UploadPartCopy response did not include an ETag.
func validateCopiedPart(part minio.CompletePart) error {
	if strings.TrimSpace(part.ETag) == "" {
		return errors.New("s3 upload-part-copy response missing etag")
	}

	return nil
}

// uploadObjectInfo converts a minio multipart-upload completion response into the port-level
// ObjectInfo, preferring the response size when S3 reported one.
func uploadObjectInfo(info minio.UploadInfo, fallbackKey string, sizeBytes int64) objectstore.ObjectInfo {
	if info.Size > 0 {
		sizeBytes = info.Size
	}

	return objectstore.ObjectInfo{
		Key:       firstNonEmpty(info.Key, fallbackKey),
		SizeBytes: sizeBytes,
		ETag:      info.ETag,
	}
}

// firstNonEmpty returns the first value in values that is not the empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

// mapError translates known S3 not-found error codes into objectstore.ErrNotFound and otherwise
// returns the original error unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	response := minio.ToErrorResponse(err)
	switch response.Code {
	case noSuchBucketCode, noSuchKeyCode, noSuchUploadCode:
		return fmt.Errorf("%w: %w", objectstore.ErrNotFound, err)
	case "":
		return err
	default:
		return err
	}
}

// mapCopyError translates copy completion errors based on whether the caller required the
// destination to be absent.
func mapCopyError(err error, params objectstore.CopyObjectParams) error {
	if !params.IfDestinationAbsent {
		return mapError(err)
	}

	return mapDestinationPreconditionError(err)
}

// mapDestinationPreconditionError maps S3 destination-conditional precondition failures to
// objectstore.ErrAlreadyExists and concurrent destination conflicts to objectstore.ErrConflict.
func mapDestinationPreconditionError(err error) error {
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case preconditionFailedCode:
		return fmt.Errorf("%w: %w", objectstore.ErrAlreadyExists, err)
	case conditionalRequestConflictCode:
		return fmt.Errorf("%w: %w", objectstore.ErrConflict, err)
	default:
		return mapError(err)
	}
}
