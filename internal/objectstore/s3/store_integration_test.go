//go:build integration

package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/objectstore"
)

const integrationPartSize = 5 * 1024 * 1024

func TestStoreMultipartObjectLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openIntegrationStore(t)

	keyPrefix := fmt.Sprintf("integration/objectstore/%d", time.Now().UTC().UnixNano())
	objectKey := keyPrefix + "/object"
	copyKey := keyPrefix + "/copy"
	abortKey := keyPrefix + "/abort"
	t.Cleanup(func() {
		_ = store.DeleteObject(context.Background(), objectstore.DeleteObjectParams{Key: objectKey})
		_ = store.DeleteObject(context.Background(), objectstore.DeleteObjectParams{Key: copyKey})
	})

	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadParams{Key: objectKey})
	require.NoError(t, err)
	assert.Equal(t, objectKey, upload.Key)
	require.NotEmpty(t, upload.UploadID)

	firstPart := bytes.Repeat([]byte("a"), integrationPartSize)
	secondPart := []byte("tail")
	partOne, err := store.PutPart(ctx, objectstore.PutPartParams{
		Key:        objectKey,
		UploadID:   upload.UploadID,
		PartNumber: 1,
		Body:       bytes.NewReader(firstPart),
		SizeBytes:  int64(len(firstPart)),
	})
	require.NoError(t, err)
	partTwo, err := store.PutPart(ctx, objectstore.PutPartParams{
		Key:        objectKey,
		UploadID:   upload.UploadID,
		PartNumber: 2,
		Body:       bytes.NewReader(secondPart),
		SizeBytes:  int64(len(secondPart)),
	})
	require.NoError(t, err)

	completed, err := store.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadParams{
		Key:      objectKey,
		UploadID: upload.UploadID,
		Parts: []objectstore.CompletePart{
			{Number: partTwo.Number, ETag: partTwo.ETag, SizeBytes: partTwo.SizeBytes},
			{Number: partOne.Number, ETag: partOne.ETag, SizeBytes: partOne.SizeBytes},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, objectKey, completed.Key)
	assert.Equal(t, int64(len(firstPart)+len(secondPart)), completed.SizeBytes)

	stat, err := store.StatObject(ctx, objectstore.StatObjectParams{Key: objectKey})
	require.NoError(t, err)
	assert.Equal(t, objectKey, stat.Key)
	assert.Equal(t, int64(len(firstPart)+len(secondPart)), stat.SizeBytes)

	reader, err := store.OpenObject(ctx, objectstore.OpenObjectParams{Key: objectKey})
	require.NoError(t, err)
	body, err := io.ReadAll(reader.Body)
	require.NoError(t, err)
	require.NoError(t, reader.Body.Close())
	assert.True(t, bytes.Equal(append(firstPart, secondPart...), body))

	rangeReader, err := store.OpenObject(ctx, objectstore.OpenObjectParams{
		Key: objectKey,
		Range: &objectstore.ByteRange{
			Start: integrationPartSize,
			End:   integrationPartSize + int64(len(secondPart)) - 1,
		},
	})
	require.NoError(t, err)
	rangeBody, err := io.ReadAll(rangeReader.Body)
	require.NoError(t, err)
	require.NoError(t, rangeReader.Body.Close())
	assert.Equal(t, secondPart, rangeBody)
	assert.Equal(t, stat.SizeBytes, rangeReader.Info.SizeBytes)

	copied, err := store.CopyObject(ctx, objectstore.CopyObjectParams{
		SourceKey:      objectKey,
		DestinationKey: copyKey,
	})
	require.NoError(t, err)
	assert.Equal(t, copyKey, copied.Key)
	assert.Equal(t, stat.SizeBytes, copied.SizeBytes)

	require.NoError(t, store.DeleteObject(ctx, objectstore.DeleteObjectParams{Key: copyKey}))
	require.NoError(t, store.DeleteObject(ctx, objectstore.DeleteObjectParams{Key: objectKey}))

	abortUpload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadParams{Key: abortKey})
	require.NoError(t, err)
	require.NoError(t, store.AbortMultipartUpload(ctx, objectstore.AbortMultipartUploadParams{
		Key:      abortKey,
		UploadID: abortUpload.UploadID,
	}))
}

func openIntegrationStore(t *testing.T) *Store {
	t.Helper()

	config := Config{
		Endpoint:        requiredEnv(t, "IMGSRV_TEST_S3_ENDPOINT"),
		Bucket:          requiredEnv(t, "IMGSRV_TEST_S3_BUCKET"),
		AccessKeyID:     requiredEnv(t, "IMGSRV_TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: requiredEnv(t, "IMGSRV_TEST_S3_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("IMGSRV_TEST_S3_SESSION_TOKEN"),
		Region:          os.Getenv("IMGSRV_TEST_S3_REGION"),
		UseTLS:          boolEnv(t, "IMGSRV_TEST_S3_USE_TLS", false),
		PathStyle:       boolEnv(t, "IMGSRV_TEST_S3_PATH_STYLE", true),
	}

	store, err := New(config)
	require.NoError(t, err)

	return store
}

func requiredEnv(t *testing.T, key string) string {
	t.Helper()

	value := os.Getenv(key)
	if value == "" {
		t.Skipf("%s is required", key)
	}

	return value
}

func boolEnv(t *testing.T, key string, defaultValue bool) bool {
	t.Helper()

	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)
	require.NoError(t, err)

	return parsed
}
