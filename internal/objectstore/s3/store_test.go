package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/objectstore"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:    "requires endpoint",
			config:  Config{},
			wantErr: "endpoint is required",
		},
		{
			name: "rejects endpoint scheme",
			config: Config{
				Endpoint:        "https://s3.example.test",
				Bucket:          "imgsrv",
				AccessKeyID:     "access-key",
				SecretAccessKey: "secret-key",
			},
			wantErr: "endpoint must not include a URL scheme",
		},
		{
			name: "requires bucket",
			config: Config{
				Endpoint:        "s3.example.test",
				AccessKeyID:     "access-key",
				SecretAccessKey: "secret-key",
			},
			wantErr: "bucket is required",
		},
		{
			name: "requires access key",
			config: Config{
				Endpoint:        "s3.example.test",
				Bucket:          "imgsrv",
				SecretAccessKey: "secret-key",
			},
			wantErr: "access key id is required",
		},
		{
			name: "requires secret key",
			config: Config{
				Endpoint:    "s3.example.test",
				Bucket:      "imgsrv",
				AccessKeyID: "access-key",
			},
			wantErr: "secret access key is required",
		},
		{
			name: "accepts complete config",
			config: Config{
				Endpoint:        "s3.example.test",
				Bucket:          "imgsrv",
				AccessKeyID:     "access-key",
				SecretAccessKey: "secret-key",
				SessionToken:    "session-token",
				Region:          "us-east-1",
				UseTLS:          true,
				PathStyle:       true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr != "" {
				require.ErrorIs(t, err, objectstore.ErrInvalid)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewConstructsStoreWithoutNetwork(t *testing.T) {
	store, err := New(Config{
		Endpoint:        "127.0.0.1:9000",
		Bucket:          "imgsrv",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		PathStyle:       true,
	})

	require.NoError(t, err)
	assert.NotNil(t, store)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	store, err := New(Config{})

	require.ErrorIs(t, err, objectstore.ErrInvalid)
	assert.Nil(t, store)
}

func TestMapErrorMapsMissingS3Resources(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{
			name: "missing bucket",
			code: noSuchBucketCode,
		},
		{
			name: "missing key",
			code: noSuchKeyCode,
		},
		{
			name: "missing upload",
			code: noSuchUploadCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapError(minio.ErrorResponse{Code: tt.code})

			require.ErrorIs(t, err, objectstore.ErrNotFound)
		})
	}
}

func TestMapErrorLeavesOtherErrorsUnchanged(t *testing.T) {
	err := errors.New("network failed")

	got := mapError(err)

	require.ErrorIs(t, got, err)
}

func TestMapDestinationPreconditionError(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		error error
	}{
		{
			name:  "existing destination",
			code:  preconditionFailedCode,
			error: objectstore.ErrAlreadyExists,
		},
		{
			name:  "concurrent destination write",
			code:  conditionalRequestConflictCode,
			error: objectstore.ErrConflict,
		},
		{
			name:  "missing source",
			code:  noSuchKeyCode,
			error: objectstore.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapDestinationPreconditionError(minio.ErrorResponse{Code: tt.code})

			require.ErrorIs(t, err, tt.error)
		})
	}
}

func TestOpenObjectRangePreservesObjectSize(t *testing.T) {
	store := newHTTPTestStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/imgsrv/object", r.URL.Path)
		assert.Equal(t, "bytes=2-4", r.Header.Get("Range"))

		writeObjectHeaders(w, 3, "range-etag")
		w.Header().Set("Content-Range", "bytes 2-4/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "234")
	}))

	reader, err := store.OpenObject(context.Background(), objectstore.OpenObjectParams{
		Key: "object",
		Range: &objectstore.ByteRange{
			Start: 2,
			End:   4,
		},
	})
	require.NoError(t, err)
	body, err := io.ReadAll(reader.Body)
	require.NoError(t, err)
	require.NoError(t, reader.Body.Close())

	assert.Equal(t, "234", string(body))
	assert.Equal(t, objectstore.ObjectInfo{
		Key:       "object",
		SizeBytes: 10,
		ETag:      "range-etag",
	}, reader.Info)
}

func TestCopyObjectIfDestinationAbsentUsesSingleCopyPrecondition(t *testing.T) {
	var ifNoneMatch string
	var sourceIfMatch string
	sourceSize := int64(4)
	store := newHTTPTestStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/imgsrv/source":
			writeObjectHeaders(w, sourceSize, "source-etag")
		case r.Method == http.MethodPut && r.URL.Path == "/imgsrv/dest" && r.URL.RawQuery == "":
			ifNoneMatch = r.Header.Get("If-None-Match")
			sourceIfMatch = r.Header.Get("X-Amz-Copy-Source-If-Match")
			assert.Equal(t, "imgsrv/source", r.Header.Get("X-Amz-Copy-Source"))
			writeXML(w, `<CopyObjectResult><ETag>"dest-etag"</ETag></CopyObjectResult>`)
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))

	copied, err := store.CopyObject(context.Background(), objectstore.CopyObjectParams{
		SourceKey:           "source",
		DestinationKey:      "dest",
		IfDestinationAbsent: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "*", ifNoneMatch)
	assert.Equal(t, "source-etag", sourceIfMatch)
	assert.Equal(t, objectstore.ObjectInfo{
		Key:       "dest",
		SizeBytes: sourceSize,
		ETag:      "dest-etag",
	}, copied)
}

func TestCopyObjectMapsSourcePreconditionFailureToConflict(t *testing.T) {
	var sourceIfMatch string
	store := newHTTPTestStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/imgsrv/source":
			writeObjectHeaders(w, 4, "source-etag")
		case r.Method == http.MethodPut && r.URL.Path == "/imgsrv/dest" && r.URL.RawQuery == "":
			sourceIfMatch = r.Header.Get("X-Amz-Copy-Source-If-Match")
			writeErrorXML(w, http.StatusPreconditionFailed, preconditionFailedCode, "source changed")
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))

	copied, err := store.CopyObject(context.Background(), objectstore.CopyObjectParams{
		SourceKey:      "source",
		DestinationKey: "dest",
	})

	require.ErrorIs(t, err, objectstore.ErrConflict)
	assert.Equal(t, "source-etag", sourceIfMatch)
	assert.Empty(t, copied)
}

func TestCopyObjectRejectsEmbeddedErrorResult(t *testing.T) {
	store := newHTTPTestStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/imgsrv/source":
			writeObjectHeaders(w, 4, "source-etag")
		case r.Method == http.MethodPut && r.URL.Path == "/imgsrv/dest" && r.URL.RawQuery == "":
			writeXML(w, `<Error><Code>InternalError</Code><Message>copy failed</Message></Error>`)
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))

	copied, err := store.CopyObject(context.Background(), objectstore.CopyObjectParams{
		SourceKey:      "source",
		DestinationKey: "dest",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "copy object response missing etag")
	assert.Empty(t, copied)
}

func TestCopyObjectLargeObjectUsesMultipartCopy(t *testing.T) {
	sourceSize := int64(maxCopyPartSizeBytes + 1)
	var ranges []string
	var sourceMatches []string
	var completeIfNoneMatch string
	store := newHTTPTestStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/imgsrv/source":
			writeObjectHeaders(w, sourceSize, "source-etag")
		case r.Method == http.MethodPost && r.URL.Path == "/imgsrv/dest" && hasQueryKey(r.URL, "uploads"):
			writeXML(w, `<InitiateMultipartUploadResult>`+
				`<Bucket>imgsrv</Bucket><Key>dest</Key><UploadId>upload-1</UploadId>`+
				`</InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && r.URL.Path == "/imgsrv/dest" &&
			r.URL.Query().Get("uploadId") == "upload-1":
			partNumber := r.URL.Query().Get("partNumber")
			ranges = append(ranges, r.Header.Get("X-Amz-Copy-Source-Range"))
			sourceMatches = append(sourceMatches, r.Header.Get("X-Amz-Copy-Source-If-Match"))
			writeXML(w, `<CopyPartResult><ETag>"part-`+partNumber+`"</ETag></CopyPartResult>`)
		case r.Method == http.MethodPost && r.URL.Path == "/imgsrv/dest" &&
			r.URL.Query().Get("uploadId") == "upload-1":
			completeIfNoneMatch = r.Header.Get("If-None-Match")
			writeXML(w, `<CompleteMultipartUploadResult>`+
				`<Bucket>imgsrv</Bucket><Key>dest</Key><ETag>"dest-etag"</ETag>`+
				`</CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))

	copied, err := store.CopyObject(context.Background(), objectstore.CopyObjectParams{
		SourceKey:           "source",
		DestinationKey:      "dest",
		IfDestinationAbsent: true,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"bytes=0-5368709119",
		"bytes=5368709120-5368709120",
	}, ranges)
	assert.Equal(t, []string{"source-etag", "source-etag"}, sourceMatches)
	assert.Equal(t, "*", completeIfNoneMatch)
	assert.Equal(t, objectstore.ObjectInfo{
		Key:       "dest",
		SizeBytes: sourceSize,
		ETag:      "dest-etag",
	}, copied)
}

func TestMultipartCopyParts(t *testing.T) {
	tests := []struct {
		name  string
		size  int64
		want  []multipartCopyPart
		error error
	}{
		{
			name: "splits object over single-copy limit",
			size: maxCopyPartSizeBytes + 1,
			want: []multipartCopyPart{
				{number: 1, start: 0, size: maxCopyPartSizeBytes},
				{number: 2, start: maxCopyPartSizeBytes, size: 1},
			},
		},
		{
			name: "allows maximum multipart-copy size",
			size: maxCopyPartSizeBytes * maxCopyPartCount,
			want: []multipartCopyPart{
				{number: 1, start: 0, size: maxCopyPartSizeBytes},
				{
					number: maxCopyPartCount,
					start:  maxCopyPartSizeBytes * (maxCopyPartCount - 1),
					size:   maxCopyPartSizeBytes,
				},
			},
		},
		{
			name:  "rejects negative size",
			size:  -1,
			error: objectstore.ErrInvalid,
		},
		{
			name:  "rejects too large object",
			size:  maxCopyPartSizeBytes*maxCopyPartCount + 1,
			error: objectstore.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := multipartCopyParts(tt.size)

			if tt.error != nil {
				require.ErrorIs(t, err, tt.error)
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, got)
			assert.Equal(t, tt.want[0], got[0])
			assert.Equal(t, tt.want[len(tt.want)-1], got[len(got)-1])
		})
	}
}

func TestRequiresMultipartCopyUsesS3SingleCopyLimit(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want bool
	}{
		{
			name: "allows exact five GB single copy",
			size: maxSingleCopySizeBytes,
		},
		{
			name: "uses multipart above five GB",
			size: maxSingleCopySizeBytes + 1,
			want: true,
		},
		{
			name: "uses multipart below five GiB part ceiling",
			size: maxCopyPartSizeBytes - 1,
			want: true,
		},
		{
			name: "uses multipart at five GiB part ceiling",
			size: maxCopyPartSizeBytes,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requiresMultipartCopy(tt.size))
		})
	}
}

func TestCompletePartsSize(t *testing.T) {
	got := completePartsSize([]objectstore.CompletePart{
		{Number: 2, ETag: "etag-2", SizeBytes: 20},
		{Number: 1, ETag: "etag-1", SizeBytes: 10},
	})

	assert.Equal(t, int64(30), got)
}

func TestUploadObjectInfoUsesKnownSizeWhenUploadInfoOmitsIt(t *testing.T) {
	got := uploadObjectInfo(minio.UploadInfo{
		Key:  "objects/test",
		ETag: "etag-1",
	}, "fallback", 42)

	assert.Equal(t, objectstore.ObjectInfo{
		Key:       "objects/test",
		SizeBytes: 42,
		ETag:      "etag-1",
	}, got)
}

func TestGetObjectOptions(t *testing.T) {
	tests := []struct {
		name      string
		params    objectstore.OpenObjectParams
		wantRange string
	}{
		{
			name:   "omits range by default",
			params: objectstore.OpenObjectParams{Key: "objects/test"},
		},
		{
			name: "sets bounded range",
			params: objectstore.OpenObjectParams{
				Key: "objects/test",
				Range: &objectstore.ByteRange{
					Start: 5,
					End:   9,
				},
			},
			wantRange: "bytes=5-9",
		},
		{
			name: "sets open-ended range",
			params: objectstore.OpenObjectParams{
				Key: "objects/test",
				Range: &objectstore.ByteRange{
					Start:     5,
					OpenEnded: true,
				},
			},
			wantRange: "bytes=5-",
		},
		{
			name: "treats open-ended zero range as whole object",
			params: objectstore.OpenObjectParams{
				Key: "objects/test",
				Range: &objectstore.ByteRange{
					OpenEnded: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := getObjectOptions(tt.params)

			require.NoError(t, err)
			assert.Equal(t, tt.wantRange, options.Header().Get("Range"))
		})
	}
}

func TestGetObjectOptionsRejectsInvalidRange(t *testing.T) {
	_, err := getObjectOptions(objectstore.OpenObjectParams{
		Key: "objects/test",
		Range: &objectstore.ByteRange{
			Start: -1,
		},
	})

	require.Error(t, err)
}

func newHTTPTestStore(t *testing.T, handler http.Handler) *Store {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	endpointURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	store, err := New(Config{
		Endpoint:        endpointURL.Host,
		Bucket:          "imgsrv",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		Region:          "us-east-1",
		PathStyle:       true,
	})
	require.NoError(t, err)

	return store
}

func writeObjectHeaders(w http.ResponseWriter, size int64, etag string) {
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC).Format(http.TimeFormat))
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, body)
}

func writeErrorXML(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<Error><Code>`+code+`</Code><Message>`+message+`</Message></Error>`)
}

func hasQueryKey(url *url.URL, key string) bool {
	_, ok := url.Query()[key]
	return ok
}
