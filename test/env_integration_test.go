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

func TestEnvWithCASPromotionPublishesReleaseFlow(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	catalog := client.Catalog()
	primaryBlob := uploadReadyBlob(ctx, t, client, []byte("public imgsrv release primary artifact"))
	attachmentBlob := uploadReadyBlob(ctx, t, client, []byte("public imgsrv release attachment"))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "public-release-flow"})
	require.NoError(t, err)
	assert.Equal(t, "public-release-flow", image.Name)

	version, err := catalog.CreateDraftVersion(ctx, image.Name, imgsrv.CreateDraftVersionRequest{
		Version: "v1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, imgsrv.ImageVersionStateDraft, version.State)

	artifact, err := catalog.AddArtifact(ctx, image.Name, version.Version, imgsrv.AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               imgsrv.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    primaryBlob.Digest,
		PrimaryBlobSizeBytes: primaryBlob.SizeBytes,
		PrimaryMediaType:     "application/x-qcow2",
	})
	require.NoError(t, err)
	assert.Equal(t, primaryBlob.Digest, artifact.PrimaryBlobDigest)

	attachment, err := catalog.AddAttachment(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		imgsrv.AddAttachmentRequest{
			Name:          "rootfs.sha256",
			MediaType:     "text/plain",
			BlobDigest:    attachmentBlob.Digest,
			BlobSizeBytes: attachmentBlob.SizeBytes,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, artifact.ID, attachment.ArtifactID)

	draft, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assertReleaseManifest(
		t,
		draft,
		image.Name,
		imgsrv.ImageVersionStateDraft,
		primaryBlob,
		attachmentBlob,
	)

	published, err := catalog.PublishVersion(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, imgsrv.ImageVersionStatePublished, published.State)
	assert.NotNil(t, published.PublishedAt)

	manifest, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assertReleaseManifest(
		t,
		manifest,
		image.Name,
		imgsrv.ImageVersionStatePublished,
		primaryBlob,
		attachmentBlob,
	)
}

type releaseBlob struct {
	Digest    imgsrv.Digest
	SizeBytes int64
}

func uploadReadyBlob(ctx context.Context, t *testing.T, client *imgsrv.Client, payload []byte) releaseBlob {
	t.Helper()

	completed := uploadPayload(ctx, t, client, payload)
	ready := waitForUploadState(ctx, t, client, completed.ID.String(), imgsrv.UploadStateReady)
	assert.Equal(t, completed.ID, ready.ID)

	return releaseBlob{
		Digest:    imgsrv.Digest(digestFor(payload)),
		SizeBytes: int64(len(payload)),
	}
}

func assertReleaseManifest(
	t *testing.T,
	manifest imgsrv.Manifest,
	imageName string,
	state imgsrv.ImageVersionState,
	primaryBlob releaseBlob,
	attachmentBlob releaseBlob,
) {
	t.Helper()

	assert.Equal(t, imageName, manifest.Image.Name)
	assert.Equal(t, state, manifest.Version.State)
	if state == imgsrv.ImageVersionStatePublished {
		assert.NotNil(t, manifest.Version.PublishedAt)
	} else {
		assert.Nil(t, manifest.Version.PublishedAt)
	}
	require.Len(t, manifest.Artifacts, 1)
	artifact := manifest.Artifacts[0].Artifact
	assert.Equal(t, "linux", artifact.OperatingSystem)
	assert.Equal(t, "x86_64", artifact.Architecture)
	assert.Equal(t, imgsrv.ArtifactFormatQCOW2, artifact.Format)
	assert.Equal(t, primaryBlob.Digest, artifact.PrimaryBlobDigest)
	assert.Equal(t, primaryBlob.SizeBytes, artifact.PrimaryBlobSizeBytes)

	require.Len(t, manifest.Artifacts[0].Attachments, 1)
	attachment := manifest.Artifacts[0].Attachments[0]
	assert.Equal(t, "rootfs.sha256", attachment.Name)
	assert.Equal(t, attachmentBlob.Digest, attachment.BlobDigest)
	assert.Equal(t, attachmentBlob.SizeBytes, attachment.BlobSizeBytes)
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
