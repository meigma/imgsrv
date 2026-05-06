//go:build integration

package imgsrvtest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
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

func TestEnvWithCASPromotionReadsBlobThroughPublicClient(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	payload := []byte("public imgsrv blob download payload")

	blob := uploadReadyBlob(ctx, t, client, payload)
	head, err := client.Blobs().HeadBlob(ctx, blob.Digest)
	require.NoError(t, err)
	assert.Equal(t, blob.Digest, head.Digest)
	assert.Equal(t, blob.SizeBytes, head.SizeBytes)
	assert.Equal(t, "bytes", head.AcceptRanges)

	suffixRange, err := imgsrv.BlobRangeSuffix(4)
	require.NoError(t, err)
	open, err := client.Blobs().OpenBlob(ctx, blob.Digest, imgsrv.OpenBlobOptions{Range: &suffixRange})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, open.Body.Close())
	}()

	body, err := io.ReadAll(open.Body)
	require.NoError(t, err)
	assert.Equal(t, string(payload[len(payload)-4:]), string(body))
	assert.Equal(t, blob.SizeBytes, open.Metadata.SizeBytes)
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

func TestEnvWithCASPromotionBrowsesAndDeletesDraftCatalog(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	catalog := client.Catalog()
	primaryBlob := uploadReadyBlob(ctx, t, client, []byte("public imgsrv browse delete primary artifact"))
	attachmentBlob := uploadReadyBlob(ctx, t, client, []byte("public imgsrv browse delete attachment"))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "public-browse-delete"})
	require.NoError(t, err)

	images, err := catalog.ListImages(ctx)
	require.NoError(t, err)
	assert.Empty(t, images)

	_, err = catalog.GetImage(ctx, image.Name)
	assertProblemStatus(t, err, http.StatusNotFound)

	firstVersion, err := catalog.CreateDraftVersion(ctx, image.Name, imgsrv.CreateDraftVersionRequest{
		Version: "v1.0.0",
	})
	require.NoError(t, err)
	_, err = catalog.CreateDraftVersion(ctx, image.Name, imgsrv.CreateDraftVersionRequest{
		Version: "v1.1.0",
	})
	require.NoError(t, err)

	_, err = catalog.ListVersions(ctx, image.Name)
	assertProblemStatus(t, err, http.StatusNotFound)

	artifact, err := catalog.AddArtifact(ctx, image.Name, firstVersion.Version, imgsrv.AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               imgsrv.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    primaryBlob.Digest,
		PrimaryBlobSizeBytes: primaryBlob.SizeBytes,
		PrimaryMediaType:     "application/x-qcow2",
	})
	require.NoError(t, err)
	attachment, err := catalog.AddAttachment(
		ctx,
		image.Name,
		firstVersion.Version,
		artifact.ID.String(),
		imgsrv.AddAttachmentRequest{
			Name:          "rootfs.sha256",
			MediaType:     "text/plain",
			BlobDigest:    attachmentBlob.Digest,
			BlobSizeBytes: attachmentBlob.SizeBytes,
		},
	)
	require.NoError(t, err)

	require.NoError(
		t,
		catalog.DeleteAttachment(ctx, image.Name, firstVersion.Version, artifact.ID.String(), attachment.ID),
	)
	manifest, err := catalog.GetVersionManifest(ctx, image.Name, firstVersion.Version)
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 1)
	assert.Empty(t, manifest.Artifacts[0].Attachments)

	require.NoError(t, catalog.DeleteArtifact(ctx, image.Name, firstVersion.Version, artifact.ID.String()))
	manifest, err = catalog.GetVersionManifest(ctx, image.Name, firstVersion.Version)
	require.NoError(t, err)
	assert.Empty(t, manifest.Artifacts)

	publishedVersion := publishVersionWithArtifact(ctx, t, catalog, image.Name, "v1.2.0", primaryBlob)
	images, err = catalog.ListImages(ctx)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, image.ID, images[0].ID)

	gotImage, err := catalog.GetImage(ctx, image.Name)
	require.NoError(t, err)
	assert.Equal(t, image.ID, gotImage.ID)

	versions, err := catalog.ListVersions(ctx, image.Name)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, publishedVersion.Version, versions[0].Version)
	assert.Equal(t, imgsrv.ImageVersionStatePublished, versions[0].State)

	publishedManifest, err := catalog.GetVersionManifest(ctx, image.Name, publishedVersion.Version)
	require.NoError(t, err)
	require.Len(t, publishedManifest.Artifacts, 1)
	err = catalog.DeleteArtifact(
		ctx,
		image.Name,
		publishedVersion.Version,
		publishedManifest.Artifacts[0].Artifact.ID.String(),
	)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)
}

func TestEnvWithCASPromotionManagesAliases(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	catalog := client.Catalog()
	primaryBlob := uploadReadyBlob(ctx, t, client, []byte("public imgsrv alias primary artifact"))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "public-alias-flow"})
	require.NoError(t, err)

	first := publishVersionWithArtifact(ctx, t, catalog, image.Name, "v1.0.0", primaryBlob)
	second := publishVersionWithArtifact(ctx, t, catalog, image.Name, "v1.1.0", primaryBlob)

	alias, err := catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: first.Version})
	require.NoError(t, err)
	assert.Equal(t, "latest", alias.Alias)
	assert.Equal(t, first.ID, alias.VersionID)
	assert.Equal(t, first.Version, alias.Version)

	gotAlias, err := catalog.GetAlias(ctx, image.Name, "latest")
	require.NoError(t, err)
	assert.Equal(t, alias.ID, gotAlias.ID)

	aliases, err := catalog.ListAliases(ctx, image.Name)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, alias.ID, aliases[0].ID)

	resolved, err := catalog.ResolveManifest(ctx, image.Name, "latest")
	require.NoError(t, err)
	assert.Equal(t, first.Version, resolved.Version.Version)

	moved, err := catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: second.Version})
	require.NoError(t, err)
	assert.Equal(t, alias.ID, moved.ID)
	assert.Equal(t, second.ID, moved.VersionID)

	resolved, err = catalog.ResolveManifest(ctx, image.Name, "latest")
	require.NoError(t, err)
	assert.Equal(t, second.Version, resolved.Version.Version)

	require.NoError(t, catalog.DeleteAlias(ctx, image.Name, "latest"))
	_, err = catalog.GetAlias(ctx, image.Name, "latest")
	assertProblemStatus(t, err, http.StatusNotFound)
	_, err = catalog.ResolveManifest(ctx, image.Name, "latest")
	assertProblemStatus(t, err, http.StatusNotFound)

	exact, err := catalog.GetVersionManifest(ctx, image.Name, first.Version)
	require.NoError(t, err)
	assert.Equal(t, first.Version, exact.Version.Version)
}

func TestEnvWithCASPromotionRejectsInvalidAliases(t *testing.T) {
	env := imgsrvtest.Start(t, imgsrvtest.WithCASPromotion())
	client := env.Client(t)
	ctx := t.Context()
	catalog := client.Catalog()

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "public-invalid-aliases"})
	require.NoError(t, err)
	draft, err := catalog.CreateDraftVersion(ctx, image.Name, imgsrv.CreateDraftVersionRequest{Version: "v1.0.0"})
	require.NoError(t, err)

	_, err = catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: draft.Version})
	assertProblemStatus(t, err, http.StatusPreconditionFailed)

	_, err = catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: "v9.9.9"})
	assertProblemStatus(t, err, http.StatusNotFound)
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

func publishVersionWithArtifact(
	ctx context.Context,
	t *testing.T,
	catalog imgsrv.CatalogClient,
	imageName string,
	version string,
	primaryBlob releaseBlob,
) imgsrv.ImageVersion {
	t.Helper()

	draft, err := catalog.CreateDraftVersion(ctx, imageName, imgsrv.CreateDraftVersionRequest{Version: version})
	require.NoError(t, err)
	_, err = catalog.AddArtifact(ctx, imageName, draft.Version, imgsrv.AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               imgsrv.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    primaryBlob.Digest,
		PrimaryBlobSizeBytes: primaryBlob.SizeBytes,
		PrimaryMediaType:     "application/x-qcow2",
	})
	require.NoError(t, err)
	published, err := catalog.PublishVersion(ctx, imageName, draft.Version)
	require.NoError(t, err)

	return published
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

func assertProblemStatus(t *testing.T, err error, status int) {
	t.Helper()

	var problem *imgsrv.ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, status, problem.HTTPStatus)
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
