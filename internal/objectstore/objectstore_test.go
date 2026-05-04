package objectstore

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateParamsRejectInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
	}{
		{
			name: "create multipart rejects empty key",
			validate: func() error {
				return CreateMultipartUploadParams{Key: " "}.Validate()
			},
		},
		{
			name: "put part rejects empty upload id",
			validate: func() error {
				return PutPartParams{
					Key:        "objects/test",
					PartNumber: 1,
					Body:       bytes.NewReader(nil),
				}.Validate()
			},
		},
		{
			name: "put part rejects invalid part number",
			validate: func() error {
				return PutPartParams{
					Key:        "objects/test",
					UploadID:   "upload-1",
					PartNumber: 0,
					Body:       bytes.NewReader(nil),
				}.Validate()
			},
		},
		{
			name: "put part rejects missing reader",
			validate: func() error {
				return PutPartParams{
					Key:        "objects/test",
					UploadID:   "upload-1",
					PartNumber: 1,
				}.Validate()
			},
		},
		{
			name: "put part rejects negative size",
			validate: func() error {
				return PutPartParams{
					Key:        "objects/test",
					UploadID:   "upload-1",
					PartNumber: 1,
					Body:       bytes.NewReader(nil),
					SizeBytes:  -1,
				}.Validate()
			},
		},
		{
			name: "put part rejects oversized part",
			validate: func() error {
				return PutPartParams{
					Key:        "objects/test",
					UploadID:   "upload-1",
					PartNumber: 1,
					Body:       bytes.NewReader(nil),
					SizeBytes:  MultipartMaxPartSizeBytes + 1,
				}.Validate()
			},
		},
		{
			name: "complete rejects empty upload id",
			validate: func() error {
				return CompleteMultipartUploadParams{
					Key: "objects/test",
					Parts: []CompletePart{{
						Number: 1,
						ETag:   "etag-1",
					}},
				}.Validate()
			},
		},
		{
			name: "abort rejects empty key",
			validate: func() error {
				return AbortMultipartUploadParams{UploadID: "upload-1"}.Validate()
			},
		},
		{
			name: "open rejects empty key",
			validate: func() error {
				return OpenObjectParams{}.Validate()
			},
		},
		{
			name: "open rejects invalid range",
			validate: func() error {
				return OpenObjectParams{
					Key: "objects/test",
					Range: &ByteRange{
						Start: 10,
						End:   9,
					},
				}.Validate()
			},
		},
		{
			name: "stat rejects empty key",
			validate: func() error {
				return StatObjectParams{}.Validate()
			},
		},
		{
			name: "copy rejects empty destination key",
			validate: func() error {
				return CopyObjectParams{SourceKey: "objects/source"}.Validate()
			},
		},
		{
			name: "delete rejects empty key",
			validate: func() error {
				return DeleteObjectParams{}.Validate()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.validate(), ErrInvalid)
		})
	}
}

func TestValidateParamsAcceptValidInput(t *testing.T) {
	require.NoError(t, CreateMultipartUploadParams{Key: "objects/test"}.Validate())
	require.NoError(t, PutPartParams{
		Key:        "objects/test",
		UploadID:   "upload-1",
		PartNumber: 1,
		Body:       bytes.NewReader(nil),
	}.Validate())
	require.NoError(t, CompleteMultipartUploadParams{
		Key:      "objects/test",
		UploadID: "upload-1",
		Parts: []CompletePart{{
			Number: 1,
			ETag:   "etag-1",
		}},
	}.Validate())
	require.NoError(t, AbortMultipartUploadParams{Key: "objects/test", UploadID: "upload-1"}.Validate())
	require.NoError(t, OpenObjectParams{Key: "objects/test"}.Validate())
	require.NoError(t, OpenObjectParams{
		Key: "objects/test",
		Range: &ByteRange{
			Start: 0,
			End:   0,
		},
	}.Validate())
	require.NoError(t, OpenObjectParams{
		Key: "objects/test",
		Range: &ByteRange{
			Start:     5,
			OpenEnded: true,
		},
	}.Validate())
	require.NoError(t, StatObjectParams{Key: "objects/test"}.Validate())
	require.NoError(t, CopyObjectParams{
		SourceKey:      "objects/source",
		DestinationKey: "objects/destination",
	}.Validate())
	require.NoError(t, DeleteObjectParams{Key: "objects/test"}.Validate())
}

func TestByteRangeValidateRejectsInvalidRanges(t *testing.T) {
	tests := []struct {
		name      string
		byteRange ByteRange
	}{
		{
			name:      "negative start",
			byteRange: ByteRange{Start: -1},
		},
		{
			name: "end before start",
			byteRange: ByteRange{
				Start: 10,
				End:   9,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.byteRange.Validate(), ErrInvalid)
		})
	}
}

func TestNormalizeCompleteParts(t *testing.T) {
	input := []CompletePart{
		{Number: 2, ETag: "etag-2", SizeBytes: 20},
		{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes},
	}

	got, err := NormalizeCompleteParts(input)

	require.NoError(t, err)
	assert.Equal(t, []CompletePart{
		{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes},
		{Number: 2, ETag: "etag-2", SizeBytes: 20},
	}, got)
	assert.Equal(t, []CompletePart{
		{Number: 2, ETag: "etag-2", SizeBytes: 20},
		{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes},
	}, input, "normalization should not mutate the caller's slice")
}

func TestNormalizeCompletePartsRejectsInvalidParts(t *testing.T) {
	tests := []struct {
		name  string
		parts []CompletePart
	}{
		{
			name:  "empty parts",
			parts: nil,
		},
		{
			name: "invalid part number",
			parts: []CompletePart{{
				Number: MultipartMaxPartNumber + 1,
				ETag:   "etag-1",
			}},
		},
		{
			name: "empty etag",
			parts: []CompletePart{{
				Number: 1,
			}},
		},
		{
			name: "negative size",
			parts: []CompletePart{{
				Number:    1,
				ETag:      "etag-1",
				SizeBytes: -1,
			}},
		},
		{
			name: "oversized part",
			parts: []CompletePart{{
				Number:    1,
				ETag:      "etag-1",
				SizeBytes: MultipartMaxPartSizeBytes + 1,
			}},
		},
		{
			name: "undersized non-final part",
			parts: []CompletePart{
				{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes - 1},
				{Number: 2, ETag: "etag-2", SizeBytes: 1},
			},
		},
		{
			name: "duplicate part number",
			parts: []CompletePart{
				{Number: 2, ETag: "etag-2", SizeBytes: MultipartMinPartSizeBytes},
				{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes},
				{Number: 2, ETag: "etag-replacement"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeCompleteParts(tt.parts)

			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestNormalizeCompletePartsAcceptsSmallFinalPart(t *testing.T) {
	got, err := NormalizeCompleteParts([]CompletePart{
		{Number: 1, ETag: "etag-1", SizeBytes: MultipartMinPartSizeBytes},
		{Number: 2, ETag: "etag-2", SizeBytes: 1},
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1), got[1].SizeBytes)
}

func TestErrorSentinelsSupportWrapping(t *testing.T) {
	require.ErrorIs(t, wrapErrInvalid(), ErrInvalid)
	require.ErrorIs(t, wrapErrAlreadyExists(), ErrAlreadyExists)
	require.ErrorIs(t, wrapErrConflict(), ErrConflict)
	require.ErrorIs(t, wrapErrNotFound(), ErrNotFound)
}

func wrapErrInvalid() error {
	return fmt.Errorf("wrapped: %w", ErrInvalid)
}

func wrapErrAlreadyExists() error {
	return fmt.Errorf("wrapped: %w", ErrAlreadyExists)
}

func wrapErrConflict() error {
	return fmt.Errorf("wrapped: %w", ErrConflict)
}

func wrapErrNotFound() error {
	return fmt.Errorf("wrapped: %w", ErrNotFound)
}
