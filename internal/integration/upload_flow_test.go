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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestUploadFlowStagesCompletedObject(t *testing.T) {
	env := harness.Start(t)
	ctx := t.Context()
	payload := []byte("imgsrv integration upload payload")
	expectedDigest := digestFor(payload)

	begin := postJSON[uploadSessionResponse](ctx, t, env, "/v1/uploads", beginUploadRequest{
		ExpectedDigest:    expectedDigest,
		ExpectedSizeBytes: int64(len(payload)),
		MediaTypeHint:     "application/octet-stream",
		FilenameHint:      "image.qcow2",
	}, http.StatusCreated)
	assert.Equal(t, expectedDigest, begin.ExpectedDigest)
	assert.Equal(t, int64(len(payload)), begin.ExpectedSizeBytes)
	assert.Equal(t, string(uploads.SessionStateCreated), begin.State)

	uploadID := parseUploadID(t, begin.ID)
	part := putUploadPart(ctx, t, env, uploadID, payload)
	assert.Equal(t, begin.ID, part.UploadID)
	assert.Equal(t, 1, part.PartNumber)
	assert.NotEmpty(t, part.ETag)
	assert.Equal(t, int64(len(payload)), part.SizeBytes)

	complete := postJSON[uploadSessionResponse](ctx, t, env, "/v1/uploads/"+begin.ID+"/complete", completeUploadRequest{
		Parts: []completeUploadPartRequest{{
			Number:    part.PartNumber,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	}, http.StatusOK)
	assert.Equal(t, begin.ID, complete.ID)
	assert.Equal(t, string(uploads.SessionStateCompleted), complete.State)

	status := getJSON[uploadSessionResponse](ctx, t, env, "/v1/uploads/"+begin.ID, http.StatusOK)
	assert.Equal(t, begin.ID, status.ID)
	assert.Equal(t, string(uploads.SessionStateCompleted), status.State)

	// This intentionally stops at completed staging. CAS worker and promotion wiring
	// are not part of the current integration flow.
	staged := readObject(ctx, t, env, uploads.StagingKey(uploadID))
	assert.Equal(t, payload, staged.Body)
	assert.Equal(t, int64(len(payload)), staged.SizeBytes)
}

type beginUploadRequest struct {
	ExpectedDigest    string `json:"expected_digest"`
	ExpectedSizeBytes int64  `json:"expected_size_bytes"`
	MediaTypeHint     string `json:"media_type_hint,omitempty"`
	FilenameHint      string `json:"filename_hint,omitempty"`
}

type completeUploadRequest struct {
	Parts []completeUploadPartRequest `json:"parts"`
}

type completeUploadPartRequest struct {
	Number    int    `json:"number"`
	ETag      string `json:"etag"`
	SizeBytes int64  `json:"size_bytes"`
}

type uploadSessionResponse struct {
	ID                string `json:"id"`
	ExpectedDigest    string `json:"expected_digest"`
	ExpectedSizeBytes int64  `json:"expected_size_bytes"`
	State             string `json:"state"`
}

type uploadPartResponse struct {
	UploadID   string `json:"upload_id"`
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	SizeBytes  int64  `json:"size_bytes"`
}

type stagedObject struct {
	Body      []byte
	SizeBytes int64
}

func postJSON[T any](
	ctx context.Context,
	t *testing.T,
	env *harness.Env,
	path string,
	requestBody any,
	wantStatus int,
) T {
	t.Helper()

	body, err := json.Marshal(requestBody)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.URL(path), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	return doJSON[T](t, env, req, wantStatus)
}

func getJSON[T any](ctx context.Context, t *testing.T, env *harness.Env, path string, wantStatus int) T {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.URL(path), nil)
	require.NoError(t, err)

	return doJSON[T](t, env, req, wantStatus)
}

func doJSON[T any](t *testing.T, env *harness.Env, req *http.Request, wantStatus int) T {
	t.Helper()

	resp, err := env.HTTPClient().Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, wantStatus, resp.StatusCode, "response body: %s", string(body))

	var decoded T
	require.NoError(t, json.Unmarshal(body, &decoded))

	return decoded
}

func putUploadPart(
	ctx context.Context,
	t *testing.T,
	env *harness.Env,
	uploadID uuid.UUID,
	body []byte,
) uploadPartResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		env.URL("/v1/uploads/"+uploadID.String()+"/parts/1"),
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	return doJSON[uploadPartResponse](t, env, req, http.StatusOK)
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
