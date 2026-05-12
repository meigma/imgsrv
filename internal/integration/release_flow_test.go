//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	"github.com/meigma/go-simplestreams/adapters/httpmirror"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imgsrv "github.com/meigma/imgsrv/client"
	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/integration/harness"
	"github.com/meigma/imgsrv/internal/jobs/promote"
	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/uploads"
)

func TestReleaseFlowPublishesDraft(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	primaryPayload := []byte("imgsrv release primary artifact")
	attachmentPayload := []byte("imgsrv release attachment")
	primaryBlob := uploadBlobToCAS(ctx, t, env, client, primaryPayload)
	attachmentBlob := uploadBlobToCAS(ctx, t, env, client, attachmentPayload)
	displayName := "Debian 12"
	description := "Integration release flow image"

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{
		Name:        "release-flow",
		DisplayName: &displayName,
		Description: &description,
	})
	require.NoError(t, err)
	assert.Equal(t, "release-flow", image.Name)
	require.NotNil(t, image.DisplayName)
	assert.Equal(t, displayName, *image.DisplayName)

	version, err := catalog.CreateDraftVersion(
		ctx,
		image.Name,
		imgsrv.CreateDraftVersionRequest{Version: "v1.0.0"},
	)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", version.Version)
	assert.Equal(t, imgsrv.ImageVersionStateDraft, version.State)

	artifact, err := catalog.AddArtifact(
		ctx,
		image.Name,
		version.Version,
		rawGZArtifactRequest(primaryBlob),
	)
	require.NoError(t, err)
	assert.Equal(t, "linux", artifact.OperatingSystem)
	assert.Equal(t, "x86_64", artifact.Architecture)
	assert.Equal(t, imgsrv.ArtifactFormatRawGZ, artifact.Format)
	assert.Equal(t, primaryBlob.Digest, artifact.PrimaryBlobDigest)
	assert.Equal(t, primaryBlob.SizeBytes, artifact.PrimaryBlobSizeBytes)

	attachment, err := catalog.AddAttachment(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		attachmentRequest("rootfs.sha256", attachmentBlob),
	)
	require.NoError(t, err)
	assert.Equal(t, artifact.ID, attachment.ArtifactID)
	assert.Equal(t, "rootfs.sha256", attachment.Name)
	assert.Equal(t, attachmentBlob.Digest, attachment.BlobDigest)
	assert.Equal(t, attachmentBlob.SizeBytes, attachment.BlobSizeBytes)

	draft, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assertManifest(t, draft, image.Name, imgsrv.ImageVersionStateDraft, primaryBlob, attachmentBlob)

	publishJob, err := catalog.PublishVersion(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.Equal(t, imgsrv.PublishJobStateQueued, publishJob.State)
	waitForPublishJob(ctx, t, catalog, publishJob.ID)

	manifest, err := catalog.GetVersionManifest(ctx, image.Name, version.Version)
	require.NoError(t, err)
	assert.NotNil(t, manifest.Version.PublishedAt)
	assertManifest(
		t,
		manifest,
		image.Name,
		imgsrv.ImageVersionStatePublished,
		primaryBlob,
		attachmentBlob,
	)

	artifacts, err := catalog.ListArtifacts(ctx, image.Name, version.Version)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, artifact.ID, artifacts[0].ID)
	assert.Equal(t, imgsrv.ArtifactFormatRawGZ, artifacts[0].Format)

	gotArtifact, err := catalog.GetArtifact(ctx, image.Name, version.Version, artifact.ID.String())
	require.NoError(t, err)
	assert.Equal(t, artifact.ID, gotArtifact.ID)
	assert.Equal(t, imgsrv.ArtifactFormatRawGZ, gotArtifact.Format)

	artifactDownload, err := catalog.OpenArtifactDownload(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		imgsrv.OpenBlobOptions{},
	)
	require.NoError(t, err)
	artifactBody, err := io.ReadAll(artifactDownload.Body)
	require.NoError(t, err)
	require.NoError(t, artifactDownload.Body.Close())
	assert.Equal(t, primaryPayload, artifactBody)
	assert.Equal(t, primaryBlob.Digest, artifactDownload.Metadata.Digest)
	assert.Equal(t, primaryBlob.SizeBytes, artifactDownload.Metadata.SizeBytes)
	assert.Equal(t, "application/gzip", artifactDownload.Metadata.ContentType)
	assertCatalogDownloadHead(
		ctx,
		t,
		env,
		catalogArtifactDownloadPath(image.Name, version.Version, artifact.ID.String()),
		primaryBlob,
		"application/gzip",
	)

	suffixRange, err := imgsrv.BlobRangeSuffix(4)
	require.NoError(t, err)
	attachmentDownload, err := catalog.OpenAttachmentDownload(
		ctx,
		image.Name,
		version.Version,
		artifact.ID.String(),
		attachment.ID,
		imgsrv.OpenBlobOptions{Range: &suffixRange},
	)
	require.NoError(t, err)
	attachmentBody, err := io.ReadAll(attachmentDownload.Body)
	require.NoError(t, err)
	require.NoError(t, attachmentDownload.Body.Close())
	assert.Equal(t, attachmentPayload[len(attachmentPayload)-4:], attachmentBody)
	assert.Equal(t, attachmentBlob.Digest, attachmentDownload.Metadata.Digest)
	assert.Equal(t, attachmentBlob.SizeBytes, attachmentDownload.Metadata.SizeBytes)
	assert.Equal(t, "text/plain", attachmentDownload.Metadata.ContentType)
	assertCatalogDownloadHead(
		ctx,
		t,
		env,
		catalogAttachmentDownloadPath(image.Name, version.Version, artifact.ID.String(), attachment.ID),
		attachmentBlob,
		"text/plain",
	)
}

func TestReleaseFlowManagesAliases(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	primaryBlob := uploadBlobToCAS(ctx, t, env, client, []byte("imgsrv alias primary artifact"))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "release-alias-flow"})
	require.NoError(t, err)

	first := createPublishedVersion(ctx, t, catalog, image.Name, "v1.0.0", primaryBlob)
	second := createPublishedVersion(ctx, t, catalog, image.Name, "v1.1.0", primaryBlob)

	exactRef, err := catalog.ResolveManifest(ctx, image.Name, first.Version)
	require.NoError(t, err)
	assert.Equal(t, first.Version, exactRef.Version.Version)
	assert.Equal(t, imgsrv.ImageVersionStatePublished, exactRef.Version.State)

	alias, err := catalog.PutAlias(
		ctx,
		image.Name,
		"latest",
		imgsrv.PutAliasRequest{Version: first.Version},
	)
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
	assert.Equal(t, imgsrv.ImageVersionStatePublished, resolved.Version.State)

	moved, err := catalog.PutAlias(
		ctx,
		image.Name,
		"latest",
		imgsrv.PutAliasRequest{Version: second.Version},
	)
	require.NoError(t, err)
	assert.Equal(t, alias.ID, moved.ID)
	assert.Equal(t, second.ID, moved.VersionID)
	assert.Equal(t, second.Version, moved.Version)

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

func TestReleaseFlowServesIncusSimpleStreams(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	diskBlob := uploadBlobToCAS(ctx, t, env, client, []byte("imgsrv incus qcow2 artifact"))
	metadataBlob := uploadBlobToCAS(ctx, t, env, client, []byte("imgsrv incus metadata artifact"))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "incus-flow"})
	require.NoError(t, err)
	draft, err := catalog.CreateDraftVersion(
		ctx,
		image.Name,
		imgsrv.CreateDraftVersionRequest{Version: "20260511_0524"},
	)
	require.NoError(t, err)
	artifact, err := catalog.AddArtifact(
		ctx,
		image.Name,
		draft.Version,
		artifactRequest(diskBlob),
	)
	require.NoError(t, err)
	_, err = catalog.AddAttachment(
		ctx,
		image.Name,
		draft.Version,
		artifact.ID.String(),
		attachmentRequest("incus.tar.xz", metadataBlob),
	)
	require.NoError(t, err)
	publishJob, err := catalog.PublishVersion(ctx, image.Name, draft.Version)
	require.NoError(t, err)
	waitForPublishJob(ctx, t, catalog, publishJob.ID)
	_, err = catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: draft.Version})
	require.NoError(t, err)

	source, err := httpmirror.New(env.BaseURL(), httpmirror.WithHTTPClient(env.HTTPClient()))
	require.NoError(t, err)
	mirror, err := simplestreams.NewMirror(source)
	require.NoError(t, err)
	index, err := mirror.Index(ctx)
	require.NoError(t, err)
	entry := index.Entries[incusschema.ContentIDImages]
	require.NotNil(t, entry)
	assert.Equal(t, simplestreams.ProductsFormat, entry.Format)
	assert.Equal(t, incusschema.DataTypeImageDownloads, entry.DataType)
	assert.Equal(t, []string{"incus-flow:20260511_0524:amd64:default"}, entry.Products)

	productFile, err := entry.ProductFile(ctx)
	require.NoError(t, err)
	require.NoError(t, incusschema.ValidateRuntimeProductFile(productFile))
	items := productFile.Items()
	require.Len(t, items, 2)
	metadataItems := simplestreams.FilterItems(items, simplestreams.MatchItemName("incus.tar.xz"))
	require.Len(t, metadataItems, 1)
	metadataItem := metadataItems[0]
	assert.Equal(t, "incus-flow/20260511_0524,incus-flow/latest", metadataItem.Metadata["aliases"])
	assert.Equal(t, "amd64", metadataItem.Metadata["arch"])
	assert.Equal(t, "incus-flow:20260511_0524:amd64:default", metadataItem.Ref.ProductName)
	assert.Equal(t, metadataBlob.SizeBytes, *metadataItem.Item.Size)
	assert.NotEmpty(t, metadataItem.Metadata["combined_disk-kvm-img_sha256"])

	diskItems := simplestreams.FilterItems(items, simplestreams.MatchItemName("disk-kvm.img"))
	require.Len(t, diskItems, 1)
	diskItem := diskItems[0]
	assert.Equal(t, "disk-kvm.img", diskItem.Item.FileType)
	assert.Equal(t, diskBlob.SizeBytes, *diskItem.Item.Size)
}

func TestReleaseFlowRetriesFailedPublishJob(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	diskPayload := []byte("imgsrv retry qcow2 artifact")
	metadataPayload := []byte("imgsrv retry incus metadata")
	diskBlob := uploadBlobToCAS(ctx, t, env, client, diskPayload)
	metadataBlob := uploadBlobToCAS(ctx, t, env, client, metadataPayload)
	metadataKey := cas.StorageKey(uploads.Digest(metadataBlob.Digest.String()))
	require.NoError(t, env.ObjectStore().DeleteObject(ctx, objectstore.DeleteObjectParams{Key: metadataKey}))

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "release-retry-publish"})
	require.NoError(t, err)
	draft, err := catalog.CreateDraftVersion(
		ctx,
		image.Name,
		imgsrv.CreateDraftVersionRequest{Version: "20260512_0001"},
	)
	require.NoError(t, err)
	artifact, err := catalog.AddArtifact(
		ctx,
		image.Name,
		draft.Version,
		artifactRequest(diskBlob),
	)
	require.NoError(t, err)
	_, err = catalog.AddAttachment(
		ctx,
		image.Name,
		draft.Version,
		artifact.ID.String(),
		attachmentRequest("incus.tar.xz", metadataBlob),
	)
	require.NoError(t, err)

	publishJob, err := catalog.PublishVersion(ctx, image.Name, draft.Version)
	require.NoError(t, err)
	failedJob := waitForPublishJobFailure(ctx, t, catalog, publishJob.ID)
	assert.Equal(t, imgsrv.PublishJobStateFailed, failedJob.State)
	require.NotNil(t, failedJob.FailureMessage)

	putObject(ctx, t, env.ObjectStore(), metadataKey, metadataPayload)
	retriedJob, err := catalog.RetryPublishJob(ctx, publishJob.ID.String())
	require.NoError(t, err)
	assert.Equal(t, imgsrv.PublishJobStateQueued, retriedJob.State)
	assert.Equal(t, imgsrv.PublishStepStateSucceeded, publishJobStepByName(t, retriedJob, "validate_catalog").State)
	assert.Equal(t, imgsrv.PublishStepStateQueued, publishJobStepByName(t, retriedJob, "incus_index").State)

	waitForPublishJob(ctx, t, catalog, publishJob.ID)
	manifest, err := catalog.GetVersionManifest(ctx, image.Name, draft.Version)
	require.NoError(t, err)
	assert.Equal(t, imgsrv.ImageVersionStatePublished, manifest.Version.State)
}

func TestReleaseFlowBrowsesAndDeletesDraftCatalog(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	primaryBlob := uploadBlobToCAS(
		ctx,
		t,
		env,
		client,
		[]byte("imgsrv browse delete primary artifact"),
	)
	attachmentBlob := uploadBlobToCAS(
		ctx,
		t,
		env,
		client,
		[]byte("imgsrv browse delete attachment"),
	)

	firstImage, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "release-browse-a"})
	require.NoError(t, err)
	_, err = catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "release-browse-b"})
	require.NoError(t, err)

	images, err := catalog.ListImages(ctx)
	require.NoError(t, err)
	assert.Empty(t, images)

	_, err = catalog.GetImage(ctx, firstImage.Name)
	assertProblemStatus(t, err, http.StatusNotFound)

	firstVersion, err := catalog.CreateDraftVersion(
		ctx,
		firstImage.Name,
		imgsrv.CreateDraftVersionRequest{
			Version: "v1.0.0",
		},
	)
	require.NoError(t, err)
	_, err = catalog.CreateDraftVersion(ctx, firstImage.Name, imgsrv.CreateDraftVersionRequest{
		Version: "v1.1.0",
	})
	require.NoError(t, err)

	_, err = catalog.ListVersions(ctx, firstImage.Name)
	assertProblemStatus(t, err, http.StatusNotFound)

	artifact, err := catalog.AddArtifact(
		ctx,
		firstImage.Name,
		firstVersion.Version,
		artifactRequest(primaryBlob),
	)
	require.NoError(t, err)
	attachment, err := catalog.AddAttachment(
		ctx,
		firstImage.Name,
		firstVersion.Version,
		artifact.ID.String(),
		attachmentRequest("rootfs.sha256", attachmentBlob),
	)
	require.NoError(t, err)

	_, err = catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: "release-browse-foreign"})
	require.NoError(t, err)
	err = catalog.DeleteArtifact(
		ctx,
		"release-browse-foreign",
		firstVersion.Version,
		artifact.ID.String(),
	)
	assertProblemStatus(t, err, http.StatusNotFound)

	require.NoError(t, catalog.DeleteAttachment(
		ctx,
		firstImage.Name,
		firstVersion.Version,
		artifact.ID.String(),
		attachment.ID,
	))
	manifest, err := catalog.GetVersionManifest(ctx, firstImage.Name, firstVersion.Version)
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 1)
	assert.Empty(t, manifest.Artifacts[0].Attachments)

	require.NoError(
		t,
		catalog.DeleteArtifact(ctx, firstImage.Name, firstVersion.Version, artifact.ID.String()),
	)
	manifest, err = catalog.GetVersionManifest(ctx, firstImage.Name, firstVersion.Version)
	require.NoError(t, err)
	assert.Empty(t, manifest.Artifacts)

	publishedVersion := createPublishedVersion(
		ctx,
		t,
		catalog,
		firstImage.Name,
		"v1.2.0",
		primaryBlob,
	)
	images, err = catalog.ListImages(ctx)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, firstImage.ID, images[0].ID)

	gotImage, err := catalog.GetImage(ctx, firstImage.Name)
	require.NoError(t, err)
	assert.Equal(t, firstImage.ID, gotImage.ID)

	versions, err := catalog.ListVersions(ctx, firstImage.Name)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, publishedVersion.Version, versions[0].Version)
	assert.Equal(t, imgsrv.ImageVersionStatePublished, versions[0].State)

	publishedManifest, err := catalog.GetVersionManifest(
		ctx,
		firstImage.Name,
		publishedVersion.Version,
	)
	require.NoError(t, err)
	require.Len(t, publishedManifest.Artifacts, 1)
	err = catalog.DeleteArtifact(
		ctx,
		firstImage.Name,
		publishedVersion.Version,
		publishedManifest.Artifacts[0].Artifact.ID.String(),
	)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)
}

func TestReleaseFlowRejectsInvalidDraftWrites(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	primaryBlob := uploadBlobToCAS(
		ctx,
		t,
		env,
		client,
		[]byte("imgsrv invalid draft primary artifact"),
	)
	attachmentBlob := uploadBlobToCAS(
		ctx,
		t,
		env,
		client,
		[]byte("imgsrv invalid draft attachment"),
	)

	_, duplicateVersion := createDraftRelease(ctx, t, catalog, "release-duplicate-writes", "v1.0.0")
	duplicateArtifact, err := catalog.AddArtifact(
		ctx,
		"release-duplicate-writes",
		duplicateVersion.Version,
		artifactRequest(primaryBlob),
	)
	require.NoError(t, err)

	_, err = catalog.AddArtifact(
		ctx,
		"release-duplicate-writes",
		duplicateVersion.Version,
		artifactRequest(primaryBlob),
	)
	assertProblemStatus(t, err, http.StatusConflict)

	_, err = catalog.AddAttachment(
		ctx,
		"release-duplicate-writes",
		duplicateVersion.Version,
		duplicateArtifact.ID.String(),
		attachmentRequest("rootfs.sha256", attachmentBlob),
	)
	require.NoError(t, err)

	_, err = catalog.AddAttachment(
		ctx,
		"release-duplicate-writes",
		duplicateVersion.Version,
		duplicateArtifact.ID.String(),
		attachmentRequest("rootfs.sha256", attachmentBlob),
	)
	assertProblemStatus(t, err, http.StatusConflict)

	_, foreignVersion := createDraftRelease(ctx, t, catalog, "release-foreign-attachment", "v1.0.0")
	_, err = catalog.AddAttachment(
		ctx,
		"release-foreign-attachment",
		foreignVersion.Version,
		duplicateArtifact.ID.String(),
		attachmentRequest("foreign.sha256", attachmentBlob),
	)
	assertProblemStatus(t, err, http.StatusNotFound)

	_, publishedVersion := createDraftRelease(
		ctx,
		t,
		catalog,
		"release-published-rejects-edits",
		"v1.0.0",
	)
	publishedArtifact, err := catalog.AddArtifact(
		ctx,
		"release-published-rejects-edits",
		publishedVersion.Version,
		artifactRequest(primaryBlob),
	)
	require.NoError(t, err)
	publishJob, err := catalog.PublishVersion(
		ctx,
		"release-published-rejects-edits",
		publishedVersion.Version,
	)
	require.NoError(t, err)
	waitForPublishJob(ctx, t, catalog, publishJob.ID)

	_, err = catalog.AddArtifact(
		ctx,
		"release-published-rejects-edits",
		publishedVersion.Version,
		artifactRequestForArchitecture("aarch64", primaryBlob),
	)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)

	_, err = catalog.AddAttachment(
		ctx,
		"release-published-rejects-edits",
		publishedVersion.Version,
		publishedArtifact.ID.String(),
		attachmentRequest("metadata.json", attachmentBlob),
	)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)
}

func TestReleaseFlowRejectsUnverifiedPublish(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()
	verifiedBlob := uploadBlobToCAS(
		ctx,
		t,
		env,
		client,
		[]byte("imgsrv size mismatch primary artifact"),
	)

	_, missingVersion := createDraftRelease(ctx, t, catalog, "release-missing-cas", "v1.0.0")
	_, err := catalog.AddArtifact(
		ctx,
		"release-missing-cas",
		missingVersion.Version,
		imgsrv.AddArtifactRequest{
			OperatingSystem: "linux",
			Architecture:    "x86_64",
			Format:          imgsrv.ArtifactFormatQCOW2,
			PrimaryBlobDigest: imgsrv.Digest(
				"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			),
			PrimaryBlobSizeBytes: 1,
			PrimaryMediaType:     "application/x-qcow2",
		},
	)
	require.NoError(t, err)
	_, err = catalog.PublishVersion(ctx, "release-missing-cas", missingVersion.Version)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)

	_, mismatchVersion := createDraftRelease(ctx, t, catalog, "release-size-mismatch", "v1.0.0")
	_, err = catalog.AddArtifact(
		ctx,
		"release-size-mismatch",
		mismatchVersion.Version,
		imgsrv.AddArtifactRequest{
			OperatingSystem:      "linux",
			Architecture:         "x86_64",
			Format:               imgsrv.ArtifactFormatQCOW2,
			PrimaryBlobDigest:    verifiedBlob.Digest,
			PrimaryBlobSizeBytes: verifiedBlob.SizeBytes + 1,
			PrimaryMediaType:     "application/x-qcow2",
		},
	)
	require.NoError(t, err)
	_, err = catalog.PublishVersion(ctx, "release-size-mismatch", mismatchVersion.Version)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)
}

func TestReleaseFlowRejectsInvalidAliases(t *testing.T) {
	env := startIntegrationEnv(t)
	ctx := t.Context()
	client := newClient(t, env)
	catalog := client.Catalog()

	image, draft := createDraftRelease(ctx, t, catalog, "release-invalid-aliases", "v1.0.0")

	_, err := catalog.PutAlias(
		ctx,
		image.Name,
		"latest",
		imgsrv.PutAliasRequest{Version: draft.Version},
	)
	assertProblemStatus(t, err, http.StatusPreconditionFailed)

	_, err = catalog.PutAlias(ctx, image.Name, "latest", imgsrv.PutAliasRequest{Version: "v9.9.9"})
	assertProblemStatus(t, err, http.StatusNotFound)
}

type catalogBlob struct {
	Digest    imgsrv.Digest
	SizeBytes int64
}

func uploadBlobToCAS(
	ctx context.Context,
	t testing.TB,
	env *harness.Env,
	client *imgsrv.Client,
	payload []byte,
) catalogBlob {
	t.Helper()

	expectedDigest := imgsrv.Digest(digestFor(payload))
	uploadsClient := client.Uploads()
	begin, err := uploadsClient.BeginUpload(ctx, imgsrv.BeginUploadRequest{
		ExpectedDigest:    expectedDigest,
		ExpectedSizeBytes: int64(len(payload)),
	})
	require.NoError(t, err)

	part, err := uploadsClient.PutUploadPart(
		ctx,
		begin.ID.String(),
		1,
		bytes.NewReader(payload),
		int64(len(payload)),
	)
	require.NoError(t, err)

	_, err = uploadsClient.CompleteUpload(ctx, begin.ID.String(), imgsrv.CompleteUploadRequest{
		Parts: []imgsrv.CompleteUploadPart{{
			Number:    part.PartNumber,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	})
	require.NoError(t, err)

	digest, err := uploads.ParseDigest(expectedDigest.String())
	require.NoError(t, err)
	casService := cas.NewService(cas.ServiceConfig{
		Store:   env.Store().CAS(),
		Objects: env.ObjectStore(),
	})
	promotion, err := promote.New(promote.Config{
		Uploads: env.Store().Uploads(),
		CAS:     casService,
	}).RunOnce(ctx, "release-flow")
	require.NoError(t, err)
	assert.True(t, promotion.Worked)

	ready, err := uploadsClient.GetUpload(ctx, begin.ID.String())
	require.NoError(t, err)
	assert.Equal(t, imgsrv.UploadStateReady, ready.State)

	blob, err := env.Store().CAS().GetBlob(ctx, cas.GetBlobParams{Digest: digest})
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), blob.SizeBytes)

	return catalogBlob{
		Digest:    expectedDigest,
		SizeBytes: int64(len(payload)),
	}
}

func createDraftRelease(
	ctx context.Context,
	t testing.TB,
	catalog imgsrv.CatalogClient,
	imageName string,
	version string,
) (imgsrv.Image, imgsrv.ImageVersion) {
	t.Helper()

	image, err := catalog.CreateImage(ctx, imgsrv.CreateImageRequest{Name: imageName})
	require.NoError(t, err)

	draft, err := catalog.CreateDraftVersion(
		ctx,
		image.Name,
		imgsrv.CreateDraftVersionRequest{Version: version},
	)
	require.NoError(t, err)

	return image, draft
}

func createPublishedVersion(
	ctx context.Context,
	t testing.TB,
	catalog imgsrv.CatalogClient,
	imageName string,
	version string,
	primaryBlob catalogBlob,
) imgsrv.ImageVersion {
	t.Helper()

	draft, err := catalog.CreateDraftVersion(
		ctx,
		imageName,
		imgsrv.CreateDraftVersionRequest{Version: version},
	)
	require.NoError(t, err)
	_, err = catalog.AddArtifact(ctx, imageName, draft.Version, artifactRequest(primaryBlob))
	require.NoError(t, err)
	publishJob, err := catalog.PublishVersion(ctx, imageName, draft.Version)
	require.NoError(t, err)
	waitForPublishJob(ctx, t, catalog, publishJob.ID)
	manifest, err := catalog.GetVersionManifest(ctx, imageName, draft.Version)
	require.NoError(t, err)

	return manifest.Version
}

func waitForPublishJob(
	ctx context.Context,
	t testing.TB,
	catalog imgsrv.CatalogClient,
	jobID imgsrv.PublishJobID,
) imgsrv.PublishJob {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		job, err := catalog.GetPublishJob(waitCtx, jobID.String())
		require.NoError(t, err)
		switch job.State {
		case imgsrv.PublishJobStateSucceeded:
			return job
		case imgsrv.PublishJobStateFailed:
			require.FailNow(t, "publish job failed", job.FailureMessage)
		}

		select {
		case <-waitCtx.Done():
			require.NoError(t, waitCtx.Err(), "timed out waiting for publish job %s", jobID)
		case <-ticker.C:
		}
	}
}

func waitForPublishJobFailure(
	ctx context.Context,
	t testing.TB,
	catalog imgsrv.CatalogClient,
	jobID imgsrv.PublishJobID,
) imgsrv.PublishJob {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		job, err := catalog.GetPublishJob(waitCtx, jobID.String())
		require.NoError(t, err)
		switch job.State {
		case imgsrv.PublishJobStateFailed:
			return job
		case imgsrv.PublishJobStateSucceeded:
			require.FailNow(t, "publish job succeeded unexpectedly", job.ID.String())
		}

		select {
		case <-waitCtx.Done():
			require.NoError(t, waitCtx.Err(), "timed out waiting for publish job %s to fail", jobID)
		case <-ticker.C:
		}
	}
}

func publishJobStepByName(t testing.TB, job imgsrv.PublishJob, name string) imgsrv.PublishJobStep {
	t.Helper()

	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	require.FailNowf(t, "missing publish job step", "job %s has no step named %s", job.ID, name)

	return imgsrv.PublishJobStep{}
}

func putObject(ctx context.Context, t testing.TB, store objectstore.Store, key string, payload []byte) {
	t.Helper()

	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadParams{Key: key})
	require.NoError(t, err)
	part, err := store.PutPart(ctx, objectstore.PutPartParams{
		Key:        key,
		UploadID:   upload.UploadID,
		PartNumber: 1,
		Body:       bytes.NewReader(payload),
		SizeBytes:  int64(len(payload)),
	})
	require.NoError(t, err)
	_, err = store.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadParams{
		Key:      key,
		UploadID: upload.UploadID,
		Parts: []objectstore.CompletePart{{
			Number:    part.Number,
			ETag:      part.ETag,
			SizeBytes: part.SizeBytes,
		}},
	})
	require.NoError(t, err)
}

func artifactRequest(blob catalogBlob) imgsrv.AddArtifactRequest {
	return artifactRequestForArchitecture("x86_64", blob)
}

func rawGZArtifactRequest(blob catalogBlob) imgsrv.AddArtifactRequest {
	request := artifactRequest(blob)
	request.Format = imgsrv.ArtifactFormatRawGZ
	request.PrimaryMediaType = "application/gzip"

	return request
}

func artifactRequestForArchitecture(
	architecture string,
	blob catalogBlob,
) imgsrv.AddArtifactRequest {
	return imgsrv.AddArtifactRequest{
		OperatingSystem:      "linux",
		Architecture:         architecture,
		Format:               imgsrv.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    blob.Digest,
		PrimaryBlobSizeBytes: blob.SizeBytes,
		PrimaryMediaType:     "application/x-qcow2",
	}
}

func attachmentRequest(name string, blob catalogBlob) imgsrv.AddAttachmentRequest {
	return imgsrv.AddAttachmentRequest{
		Name:          name,
		MediaType:     "text/plain",
		BlobDigest:    blob.Digest,
		BlobSizeBytes: blob.SizeBytes,
	}
}

func catalogArtifactDownloadPath(imageName string, version string, artifactID string) string {
	return "/v1/images/" + url.PathEscape(imageName) +
		"/versions/" + url.PathEscape(version) +
		"/artifacts/" + url.PathEscape(artifactID) +
		"/download"
}

func catalogAttachmentDownloadPath(
	imageName string,
	version string,
	artifactID string,
	attachmentID string,
) string {
	return "/v1/images/" + url.PathEscape(imageName) +
		"/versions/" + url.PathEscape(version) +
		"/artifacts/" + url.PathEscape(artifactID) +
		"/attachments/" + url.PathEscape(attachmentID) +
		"/download"
}

func assertCatalogDownloadHead(
	ctx context.Context,
	t testing.TB,
	env *harness.Env,
	path string,
	blob catalogBlob,
	contentType string,
) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, env.URL(path), nil)
	require.NoError(t, err)
	resp, err := env.HTTPClient().Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	assert.Equal(t, strconv.FormatInt(blob.SizeBytes, 10), resp.Header.Get("Content-Length"))
	assert.Equal(t, contentType, resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func assertManifest(
	t testing.TB,
	manifest imgsrv.Manifest,
	imageName string,
	state imgsrv.ImageVersionState,
	primaryBlob catalogBlob,
	attachmentBlob catalogBlob,
) {
	t.Helper()

	assert.Equal(t, imageName, manifest.Image.Name)
	assert.Equal(t, state, manifest.Version.State)
	require.Len(t, manifest.Artifacts, 1)
	artifact := manifest.Artifacts[0].Artifact
	assert.Equal(t, imgsrv.ArtifactFormatRawGZ, artifact.Format)
	assert.Equal(t, "application/gzip", artifact.PrimaryMediaType)
	assert.Equal(t, primaryBlob.Digest, artifact.PrimaryBlobDigest)
	assert.Equal(t, primaryBlob.SizeBytes, artifact.PrimaryBlobSizeBytes)
	require.Len(t, manifest.Artifacts[0].Attachments, 1)
	attachment := manifest.Artifacts[0].Attachments[0]
	assert.Equal(t, attachmentBlob.Digest, attachment.BlobDigest)
	assert.Equal(t, attachmentBlob.SizeBytes, attachment.BlobSizeBytes)
}

func assertProblemStatus(t testing.TB, err error, status int) {
	t.Helper()

	var problem *imgsrv.ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, status, problem.HTTPStatus)
}
