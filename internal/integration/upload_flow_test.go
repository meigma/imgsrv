//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestUploadFlowStagesCompletedObject(t *testing.T) {
	env := harness.Start(t)
	ctx := t.Context()
	payload := []byte("imgsrv integration upload payload")
	expectedDigest := imgsrv.Digest(digestFor(payload))
	client := newClient(t, env)
	uploadsClient := client.Uploads()
	mediaType := "application/octet-stream"
	filename := "image.qcow2"

	begin, err := uploadsClient.BeginUpload(ctx, imgsrv.BeginUploadRequest{
		ExpectedDigest:    expectedDigest,
		ExpectedSizeBytes: int64(len(payload)),
		MediaTypeHint:     &mediaType,
		FilenameHint:      &filename,
	})
	require.NoError(t, err)
	assert.Equal(t, expectedDigest, begin.ExpectedDigest)
	assert.Equal(t, int64(len(payload)), begin.ExpectedSizeBytes)
	assert.Equal(t, imgsrv.UploadStateCreated, begin.State)

	uploadID := parseUploadID(t, begin.ID.String())
	part, err := uploadsClient.PutUploadPart(ctx, begin.ID.String(), 1, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	assert.Equal(t, begin.ID, part.UploadID)
	assert.Equal(t, 1, part.PartNumber)
	assert.NotEmpty(t, part.ETag)
	assert.Equal(t, int64(len(payload)), part.SizeBytes)

	complete, err := uploadsClient.CompleteUpload(ctx, begin.ID.String(), imgsrv.CompleteUploadRequest{
		Parts: []imgsrv.CompleteUploadPart{{
			Number:    part.PartNumber,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, begin.ID, complete.ID)
	assert.Equal(t, imgsrv.UploadStateCompleted, complete.State)

	status, err := uploadsClient.GetUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, begin.ID, status.ID)
	assert.Equal(t, imgsrv.UploadStateCompleted, status.State)

	staged := readObject(ctx, t, env, uploads.StagingKey(uploadID))
	assert.Equal(t, payload, staged.Body)
	assert.Equal(t, int64(len(payload)), staged.SizeBytes)

	digest, err := uploads.ParseDigest(expectedDigest.String())
	require.NoError(t, err)
	casService := cas.NewService(cas.ServiceConfig{
		Store:   env.Store().CAS(),
		Objects: env.ObjectStore(),
	})
	promotionJob := promote.New(promote.Config{
		Uploads: env.Store().Uploads(),
		CAS:     casService,
	})
	promotion, err := promotionJob.RunOnce(ctx, "integration-test")
	require.NoError(t, err)
	assert.True(t, promotion.Worked)

	ready, err := uploadsClient.GetUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, begin.ID, ready.ID)
	assert.Equal(t, imgsrv.UploadStateReady, ready.State)

	blob, err := env.Store().CAS().GetBlob(ctx, cas.GetBlobParams{Digest: digest})
	require.NoError(t, err)
	assert.Equal(t, digest, blob.Digest)
	assert.Equal(t, int64(len(payload)), blob.SizeBytes)

	casObject := readObject(ctx, t, env, cas.StorageKey(digest))
	assert.Equal(t, payload, casObject.Body)
	assert.Equal(t, int64(len(payload)), casObject.SizeBytes)

	reader, err := casService.OpenBlob(ctx, cas.OpenBlobParams{Digest: digest})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reader.Body.Close())
	}()
	opened, err := io.ReadAll(reader.Body)
	require.NoError(t, err)
	assert.Equal(t, payload, opened)
}

type stagedObject struct {
	Body      []byte
	SizeBytes int64
}

func newClient(t *testing.T, env *harness.Env) *imgsrv.Client {
	t.Helper()

	client, err := imgsrv.New(imgsrv.Options{
		BaseURL:    env.BaseURL(),
		HTTPClient: env.HTTPClient(),
		UserAgent:  "imgsrv-integration-test",
	})
	require.NoError(t, err)

	return client
}

func readObject(ctx context.Context, t *testing.T, env *harness.Env, key string) stagedObject {
	t.Helper()

	reader, err := env.ObjectStore().OpenObject(ctx, objectstore.OpenObjectParams{Key: key})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reader.Body.Close())
	}()

	body, err := io.ReadAll(reader.Body)
	require.NoError(t, err)

	return stagedObject{
		Body:      body,
		SizeBytes: reader.Info.SizeBytes,
	}
}

func digestFor(payload []byte) string {
	sum := sha256.Sum256(payload)

	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseUploadID(t *testing.T, raw string) uuid.UUID {
	t.Helper()

	id, err := uuid.Parse(raw)
	require.NoError(t, err)

	return id
}
