//go:build integration

package imgsrvtest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	imgsrvtest "github.com/meigma/imgsrv/test"
)

const (
	readyTimeout  = 5 * time.Second
	readyInterval = 25 * time.Millisecond
)

func TestEnvDrivesUploadThroughPublicClient(t *testing.T) {
	env := imgsrvtest.Start(t)
	client := env.Client(t)
	ctx := t.Context()
	payload := []byte("public imgsrv test upload payload")

	completed := uploadPayload(ctx, t, client, payload)

	assert.Equal(t, imgsrv.UploadStateCompleted, completed.State)
	status, err := client.Uploads().GetUpload(ctx, completed.ID.String())
	require.NoError(t, err)
	assert.Equal(t, completed.ID, status.ID)
	assert.Equal(t, imgsrv.UploadStateCompleted, status.State)
}

func TestEnvWithCASPromotionPromotesUpload(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	payload := []byte("public imgsrv test cas promotion payload")

	completed := uploadPayload(ctx, t, client, payload)
	ready := waitForUploadState(ctx, t, client, completed.ID.String(), imgsrv.UploadStateReady)

	assert.Equal(t, completed.ID, ready.ID)
	assert.Equal(t, imgsrv.UploadStateReady, ready.State)
}

func uploadPayload(ctx context.Context, t *testing.T, client *imgsrv.Client, payload []byte) imgsrv.UploadSession {
	t.Helper()

	uploads := client.Uploads()
	expectedDigest := imgsrv.Digest(digestFor(payload))
	begin, err := uploads.BeginUpload(ctx, imgsrv.BeginUploadRequest{
		ExpectedDigest:    expectedDigest,
		ExpectedSizeBytes: int64(len(payload)),
	})
	require.NoError(t, err)
	assert.Equal(t, expectedDigest, begin.ExpectedDigest)
	assert.Equal(t, int64(len(payload)), begin.ExpectedSizeBytes)
	assert.Equal(t, imgsrv.UploadStateCreated, begin.State)

	part, err := uploads.PutUploadPart(ctx, begin.ID.String(), 1, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	assert.Equal(t, begin.ID, part.UploadID)
	assert.Equal(t, 1, part.PartNumber)
	assert.NotEmpty(t, part.ETag)
	assert.Equal(t, int64(len(payload)), part.SizeBytes)

	complete, err := uploads.CompleteUpload(ctx, begin.ID.String(), imgsrv.CompleteUploadRequest{
		Parts: []imgsrv.CompleteUploadPart{{
			Number:    part.PartNumber,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, begin.ID, complete.ID)
	assert.Equal(t, imgsrv.UploadStateCompleted, complete.State)

	return complete
}

func waitForUploadState(
	ctx context.Context,
	t *testing.T,
	client *imgsrv.Client,
	uploadID string,
	want imgsrv.UploadState,
) imgsrv.UploadSession {
	t.Helper()

	deadline := time.NewTimer(readyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(readyInterval)
	defer ticker.Stop()

	var last imgsrv.UploadSession
	var lastErr error
	for {
		last, lastErr = client.Uploads().GetUpload(ctx, uploadID)
		if lastErr == nil && last.State == want {
			return last
		}

		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case <-deadline.C:
			require.NoError(t, lastErr)
			require.Equal(t, want, last.State)
		case <-ticker.C:
		}
	}
}

func digestFor(payload []byte) string {
	sum := sha256.Sum256(payload)

	return "sha256:" + hex.EncodeToString(sum[:])
}
