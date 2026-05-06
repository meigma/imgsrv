//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

	_, err = env.ObjectStore().OpenObject(ctx, objectstore.OpenObjectParams{Key: uploads.StagingKey(uploadID)})
	require.ErrorIs(t, err, objectstore.ErrNotFound)

	reader, err := casService.OpenBlob(ctx, cas.OpenBlobParams{Digest: digest})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reader.Body.Close())
	}()
	opened, err := io.ReadAll(reader.Body)
	require.NoError(t, err)
	assert.Equal(t, payload, opened)
}

func TestUploadFlowSkipsTrustedDigestAndLeavesNoQueuedJob(t *testing.T) {
	env := harness.Start(t)
	ctx := t.Context()
	client := newClient(t, env)
	payload := []byte("imgsrv integration trusted digest payload")
	blob := uploadBlobToCAS(ctx, t, env, client, payload)

	skippedByClient, err := client.Uploads().BeginUpload(ctx, imgsrv.BeginUploadRequest{
		ExpectedDigest:    blob.Digest,
		ExpectedSizeBytes: blob.SizeBytes,
	})
	require.NoError(t, err)
	assert.Equal(t, imgsrv.UploadStateReady, skippedByClient.State)
	assert.Equal(t, blob.Digest, skippedByClient.ExpectedDigest)
	assert.Equal(t, blob.SizeBytes, skippedByClient.ExpectedSizeBytes)

	body := bytes.NewBufferString(
		`{"expected_digest":"` + blob.Digest.String() + `","expected_size_bytes":` + strconv.FormatInt(
			blob.SizeBytes,
			10,
		) + `}`,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.URL("/v1/uploads"), body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.HTTPClient().Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var skipped imgsrv.UploadSession
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&skipped))
	assert.Equal(t, imgsrv.UploadStateReady, skipped.State)
	assert.Equal(t, blob.Digest, skipped.ExpectedDigest)
	assert.Equal(t, blob.SizeBytes, skipped.ExpectedSizeBytes)

	status, err := client.Uploads().GetUpload(ctx, skippedByClient.ID.String())
	require.NoError(t, err)
	assert.Equal(t, skippedByClient.ID, status.ID)
	assert.Equal(t, imgsrv.UploadStateReady, status.State)

	_, err = env.Store().Uploads().ClaimIngestJob(ctx, uploads.ClaimIngestJobParams{WorkerID: "skip-check"})
	require.ErrorIs(t, err, uploads.ErrNotFound)
}

func TestBlobRouteSupportsHeadAndRanges(t *testing.T) {
	env := harness.Start(t)
	ctx := t.Context()
	client := newClient(t, env)
	payload := []byte("imgsrv integration ranged blob payload")
	blob := uploadBlobToCAS(ctx, t, env, client, payload)
	blobURL := env.URL("/v1/blobs/" + url.PathEscape(blob.Digest.String()))

	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, blobURL, nil)
	require.NoError(t, err)
	headResp, err := env.HTTPClient().Do(headReq)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, headResp.Body.Close())
	}()
	assert.Equal(t, http.StatusOK, headResp.StatusCode)
	assert.Equal(t, "bytes", headResp.Header.Get("Accept-Ranges"))
	assert.Equal(t, strconv.FormatInt(blob.SizeBytes, 10), headResp.Header.Get("Content-Length"))

	rangeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	require.NoError(t, err)
	rangeReq.Header.Set("Range", "bytes=-4")
	rangeResp, err := env.HTTPClient().Do(rangeReq)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, rangeResp.Body.Close())
	}()
	rangeBody, err := io.ReadAll(rangeResp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, rangeResp.StatusCode)
	assert.Equal(
		t,
		"bytes "+strconv.FormatInt(
			blob.SizeBytes-4,
			10,
		)+"-"+strconv.FormatInt(
			blob.SizeBytes-1,
			10,
		)+"/"+strconv.FormatInt(
			blob.SizeBytes,
			10,
		),
		rangeResp.Header.Get("Content-Range"),
	)
	assert.Equal(t, string(payload[len(payload)-4:]), string(rangeBody))

	suffixRange, err := imgsrv.BlobRangeSuffix(4)
	require.NoError(t, err)
	open, err := client.Blobs().OpenBlob(ctx, blob.Digest, imgsrv.OpenBlobOptions{Range: &suffixRange})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, open.Body.Close())
	}()
	openedBody, err := io.ReadAll(open.Body)
	require.NoError(t, err)
	assert.Equal(t, string(payload[len(payload)-4:]), string(openedBody))
	assert.Equal(t, blob.SizeBytes, open.Metadata.SizeBytes)
}

func TestUploadFlowAbortsMutableUpload(t *testing.T) {
	env := harness.Start(t)
	ctx := t.Context()
	payload := []byte("imgsrv integration abort upload payload")
	expectedDigest := imgsrv.Digest(digestFor(payload))
	client := newClient(t, env)
	uploadsClient := client.Uploads()

	begin, err := uploadsClient.BeginUpload(ctx, imgsrv.BeginUploadRequest{
		ExpectedDigest:    expectedDigest,
		ExpectedSizeBytes: int64(len(payload)),
	})
	require.NoError(t, err)
	assert.Equal(t, imgsrv.UploadStateCreated, begin.State)

	part, err := uploadsClient.PutUploadPart(ctx, begin.ID.String(), 1, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	assert.Equal(t, begin.ID, part.UploadID)

	aborted, err := uploadsClient.AbortUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, begin.ID, aborted.ID)
	assert.Equal(t, imgsrv.UploadStateAborted, aborted.State)

	status, err := uploadsClient.GetUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, begin.ID, status.ID)
	assert.Equal(t, imgsrv.UploadStateAborted, status.State)

	_, err = uploadsClient.CompleteUpload(ctx, begin.ID.String(), imgsrv.CompleteUploadRequest{
		Parts: []imgsrv.CompleteUploadPart{{
			Number:    part.PartNumber,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	})
	var problem *imgsrv.ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, http.StatusPreconditionFailed, problem.HTTPStatus)
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
