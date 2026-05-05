// Package objectstore defines provider-neutral object storage primitives.
package objectstore

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	// MultipartMinPartSizeBytes is the minimum size for every non-final multipart part.
	MultipartMinPartSizeBytes int64 = 5 * 1024 * 1024

	// MultipartMaxPartSizeBytes is the maximum size for any multipart part.
	MultipartMaxPartSizeBytes int64 = 5 * 1024 * 1024 * 1024

	// MultipartMaxPartNumber is the highest S3-compatible multipart part number.
	MultipartMaxPartNumber = 10000
)

// Error identifies a category of object-store failure.
type Error string

// Error returns the error kind text.
func (kind Error) Error() string {
	return string(kind)
}

const (
	// ErrInvalid means the request contains invalid input.
	ErrInvalid Error = "objectstore invalid input"

	// ErrAlreadyExists means the requested write would replace an existing object.
	ErrAlreadyExists Error = "objectstore already exists"

	// ErrConflict means the requested write hit a concurrent object-store conflict.
	ErrConflict Error = "objectstore conflict"

	// ErrNotFound means the requested object-store resource does not exist.
	ErrNotFound Error = "objectstore not found"
)

// Store exposes bucket-relative object storage operations.
type Store interface {
	// CreateMultipartUpload starts a multipart write for a bucket-relative key.
	CreateMultipartUpload(context.Context, CreateMultipartUploadParams) (MultipartUpload, error)

	// PutPart writes or replaces one part in an active multipart upload.
	PutPart(context.Context, PutPartParams) (Part, error)

	// CompleteMultipartUpload commits an active multipart upload into an object.
	CompleteMultipartUpload(context.Context, CompleteMultipartUploadParams) (ObjectInfo, error)

	// AbortMultipartUpload discards an active multipart upload.
	AbortMultipartUpload(context.Context, AbortMultipartUploadParams) error

	// OpenObject opens a stored object, optionally constrained to a byte range.
	OpenObject(context.Context, OpenObjectParams) (ObjectReader, error)

	// StatObject returns metadata for a stored object.
	StatObject(context.Context, StatObjectParams) (ObjectInfo, error)

	// CopyObject copies an object to another bucket-relative key.
	CopyObject(context.Context, CopyObjectParams) (ObjectInfo, error)

	// DeleteObject removes a stored object.
	DeleteObject(context.Context, DeleteObjectParams) error
}

// MultipartUpload identifies an in-progress multipart object write.
type MultipartUpload struct {
	// Key is the bucket-relative object key being written.
	Key string

	// UploadID is the provider multipart upload identity.
	UploadID string
}

// Part describes an uploaded multipart object part.
type Part struct {
	// Number is the S3-compatible multipart part number.
	Number int

	// ETag is the provider entity tag for the uploaded part.
	ETag string

	// SizeBytes is the accepted part size.
	SizeBytes int64
}

// CompletePart identifies a multipart object part to commit.
type CompletePart struct {
	// Number is the S3-compatible multipart part number.
	Number int

	// ETag is the provider entity tag for the uploaded part.
	ETag string

	// SizeBytes is the accepted part size.
	SizeBytes int64
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	// Key is the bucket-relative object key.
	Key string

	// SizeBytes is the object size.
	SizeBytes int64

	// ETag is the provider entity tag for the object.
	ETag string
}

// ObjectReader streams an object with its metadata.
type ObjectReader struct {
	// Info describes the opened object.
	Info ObjectInfo

	// Body streams the object bytes.
	Body io.ReadCloser
}

// CreateMultipartUploadParams starts a multipart object write.
type CreateMultipartUploadParams struct {
	// Key is the bucket-relative object key to write.
	Key string
}

// Validate checks that params can start a multipart object write.
func (params CreateMultipartUploadParams) Validate() error {
	return ValidateKey(params.Key)
}

// PutPartParams uploads or replaces one multipart object part.
type PutPartParams struct {
	// Key is the bucket-relative object key being written.
	Key string

	// UploadID is the provider multipart upload identity.
	UploadID string

	// PartNumber is the S3-compatible multipart part number.
	PartNumber int

	// Body streams the part bytes.
	Body io.Reader

	// SizeBytes is the part size.
	SizeBytes int64
}

// Validate checks that params can upload one multipart object part.
func (params PutPartParams) Validate() error {
	if err := ValidateKey(params.Key); err != nil {
		return err
	}
	if err := ValidateRequiredText("upload id", params.UploadID); err != nil {
		return err
	}
	if err := ValidatePartNumber(params.PartNumber); err != nil {
		return err
	}
	if params.Body == nil {
		return fmt.Errorf("%w: body is required", ErrInvalid)
	}
	return ValidateMultipartPartSize("part size", params.SizeBytes)
}

// CompleteMultipartUploadParams commits an in-progress multipart object write.
type CompleteMultipartUploadParams struct {
	// Key is the bucket-relative object key being written.
	Key string

	// UploadID is the provider multipart upload identity.
	UploadID string

	// Parts are the uploaded parts to commit.
	Parts []CompletePart
}

// Validate checks that params can commit a multipart object write.
func (params CompleteMultipartUploadParams) Validate() error {
	if err := ValidateKey(params.Key); err != nil {
		return err
	}
	if err := ValidateRequiredText("upload id", params.UploadID); err != nil {
		return err
	}
	_, err := NormalizeCompleteParts(params.Parts)
	return err
}

// AbortMultipartUploadParams aborts an in-progress multipart object write.
type AbortMultipartUploadParams struct {
	// Key is the bucket-relative object key being written.
	Key string

	// UploadID is the provider multipart upload identity.
	UploadID string
}

// Validate checks that params can abort a multipart object write.
func (params AbortMultipartUploadParams) Validate() error {
	if err := ValidateKey(params.Key); err != nil {
		return err
	}
	return ValidateRequiredText("upload id", params.UploadID)
}

// OpenObjectParams opens a stored object for reading.
type OpenObjectParams struct {
	// Key is the bucket-relative object key to open.
	Key string

	// Range optionally limits the bytes returned by Body. Nil opens the whole object.
	Range *ByteRange
}

// Validate checks that params can open an object.
func (params OpenObjectParams) Validate() error {
	if err := ValidateKey(params.Key); err != nil {
		return err
	}
	if params.Range == nil {
		return nil
	}
	return params.Range.Validate()
}

// ByteRange describes an inclusive byte range to read from an object.
type ByteRange struct {
	// Start is the zero-based first byte to read.
	Start int64

	// End is the inclusive final byte to read. It is ignored when OpenEnded is true.
	End int64

	// OpenEnded reads from Start through the end of the object.
	OpenEnded bool
}

// Validate checks that byteRange is a valid object read range.
func (byteRange ByteRange) Validate() error {
	if byteRange.Start < 0 {
		return fmt.Errorf("%w: range start must be non-negative", ErrInvalid)
	}
	if byteRange.OpenEnded {
		return nil
	}
	if byteRange.End < byteRange.Start {
		return fmt.Errorf("%w: range end must be greater than or equal to range start", ErrInvalid)
	}

	return nil
}

// StatObjectParams looks up stored object metadata.
type StatObjectParams struct {
	// Key is the bucket-relative object key to inspect.
	Key string
}

// Validate checks that params can inspect an object.
func (params StatObjectParams) Validate() error {
	return ValidateKey(params.Key)
}

// CopyObjectParams copies a stored object to another key.
type CopyObjectParams struct {
	// SourceKey is the bucket-relative object key to copy from.
	SourceKey string

	// IfSourceETag copies only when the source object matches this ETag.
	// Stores return ErrConflict when the provider reports a source mismatch.
	IfSourceETag string

	// DestinationKey is the bucket-relative object key to copy to.
	DestinationKey string

	// IfDestinationAbsent copies only when DestinationKey does not already exist.
	// Stores return ErrAlreadyExists when the destination exists and ErrConflict
	// when the provider reports a concurrent conditional-write conflict.
	IfDestinationAbsent bool
}

// Validate checks that params can copy an object.
func (params CopyObjectParams) Validate() error {
	if err := ValidateKey(params.SourceKey); err != nil {
		return err
	}
	return ValidateKey(params.DestinationKey)
}

// DeleteObjectParams deletes a stored object.
type DeleteObjectParams struct {
	// Key is the bucket-relative object key to delete.
	Key string
}

// Validate checks that params can delete an object.
func (params DeleteObjectParams) Validate() error {
	return ValidateKey(params.Key)
}

// NormalizeCompleteParts validates and sorts parts for multipart completion.
func NormalizeCompleteParts(parts []CompletePart) ([]CompletePart, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: at least one complete part is required", ErrInvalid)
	}

	normalized := slices.Clone(parts)
	slices.SortFunc(normalized, func(left CompletePart, right CompletePart) int {
		return left.Number - right.Number
	})

	for index, part := range normalized {
		if err := ValidatePartNumber(part.Number); err != nil {
			return nil, err
		}
		if err := ValidateRequiredText("etag", part.ETag); err != nil {
			return nil, err
		}
		if err := ValidateMultipartPartSize("part size", part.SizeBytes); err != nil {
			return nil, err
		}
		if index < len(normalized)-1 && part.SizeBytes < MultipartMinPartSizeBytes {
			return nil, fmt.Errorf(
				"%w: non-final part size must be at least %d bytes",
				ErrInvalid,
				MultipartMinPartSizeBytes,
			)
		}
		if index > 0 && normalized[index-1].Number == part.Number {
			return nil, fmt.Errorf("%w: duplicate complete part %d", ErrInvalid, part.Number)
		}
	}

	return normalized, nil
}

// ValidateKey validates a bucket-relative object key.
func ValidateKey(key string) error {
	return ValidateRequiredText("key", key)
}

// ValidatePartNumber validates an S3-compatible multipart part number.
func ValidatePartNumber(partNumber int) error {
	if partNumber < 1 || partNumber > MultipartMaxPartNumber {
		return fmt.Errorf("%w: part number must be between 1 and %d", ErrInvalid, MultipartMaxPartNumber)
	}

	return nil
}

// ValidateMultipartPartSize validates that size fits S3 multipart part bounds.
func ValidateMultipartPartSize(field string, size int64) error {
	if err := ValidateNonNegativeSize(field, size); err != nil {
		return err
	}
	if size > MultipartMaxPartSizeBytes {
		return fmt.Errorf("%w: %s must be no greater than %d bytes", ErrInvalid, field, MultipartMaxPartSizeBytes)
	}

	return nil
}

// ValidateNonNegativeSize validates that size is not negative.
func ValidateNonNegativeSize(field string, size int64) error {
	if size < 0 {
		return fmt.Errorf("%w: %s must be non-negative", ErrInvalid, field)
	}

	return nil
}

// ValidateRequiredText validates non-empty text after trimming ASCII whitespace.
func ValidateRequiredText(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}

	return nil
}
