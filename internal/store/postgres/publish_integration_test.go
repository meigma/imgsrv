//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogdomain "github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/materialization/incus"
	publishdomain "github.com/meigma/imgsrv/internal/publish"
)

func TestPublishWorkflowQueuesStepsAndFinalizesVersion(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()
	publishStore := store.Publish()

	image, err := catalogStore.CreateImage(ctx, catalogdomain.CreateImageParams{Name: "publish-flow"})
	require.NoError(t, err)
	version, err := catalogStore.CreateDraftVersion(ctx, catalogdomain.CreateDraftVersionParams{
		ImageName: image.Name,
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	diskDigest := catalogDigest("a")
	metadataDigest := catalogDigest("b")
	insertTrustedBlob(t, store, diskDigest, 4096)
	insertTrustedBlob(t, store, metadataDigest, 512)
	artifact, err := catalogStore.AddArtifact(ctx, catalogdomain.AddArtifactParams{
		ImageName:            image.Name,
		Version:              version.Version,
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               catalogdomain.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    diskDigest,
		PrimaryBlobSizeBytes: 4096,
		PrimaryMediaType:     "application/x-qcow2",
	})
	require.NoError(t, err)
	attachment, err := catalogStore.AddAttachment(ctx, catalogdomain.AddAttachmentParams{
		ImageName:     image.Name,
		Version:       version.Version,
		ArtifactID:    artifact.ID,
		Name:          "incus.tar.xz",
		MediaType:     "application/x-xz",
		BlobDigest:    metadataDigest,
		BlobSizeBytes: 512,
	})
	require.NoError(t, err)

	job, err := publishStore.EnqueueVersion(ctx, publishdomain.EnqueueVersionParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, publishdomain.JobStateQueued, job.State)
	require.Len(t, job.Steps, 3)
	assert.Equal(t, []string{
		publishdomain.StepValidateCatalog,
		publishdomain.StepIncusIndex,
		publishdomain.StepFinalizePublish,
	}, []string{job.Steps[0].Name, job.Steps[1].Name, job.Steps[2].Name})

	manifest, err := catalogStore.GetVersionManifest(ctx, catalogdomain.GetVersionManifestParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, catalogdomain.VersionStatePublishing, manifest.Version.State)
	_, err = catalogStore.AddArtifact(ctx, catalogdomain.AddArtifactParams{
		ImageName:            image.Name,
		Version:              version.Version,
		OperatingSystem:      "linux",
		Architecture:         "aarch64",
		Format:               catalogdomain.ArtifactFormatRaw,
		PrimaryBlobDigest:    diskDigest,
		PrimaryBlobSizeBytes: 4096,
		PrimaryMediaType:     "application/octet-stream",
	})
	assert.ErrorIs(t, err, catalogdomain.ErrFailedPrecondition)

	validateStep := claimStep(t, ctx, publishStore, publishdomain.StepValidateCatalog)
	_, err = publishStore.SucceedValidateCatalogStep(ctx, succeedStepParams(validateStep))
	require.NoError(t, err)

	incusStep := claimStep(t, ctx, publishStore, publishdomain.StepIncusIndex)
	_, err = publishStore.SucceedIncusIndexStep(ctx, publishdomain.SucceedIncusIndexStepParams{
		ID:           incusStep.ID,
		WorkerID:     testPublishWorkerID,
		AttemptCount: incusStep.AttemptCount,
		VersionID:    version.ID,
		Rows: []incus.ProjectionRow{
			{
				VersionID:                version.ID,
				ArtifactID:               artifact.ID,
				MetadataAttachmentID:     attachment.ID,
				MetadataPath:             "v1/images/publish-flow/versions/v1.0.0/artifacts/" + artifact.ID.String() + "/attachments/" + attachment.ID.String() + "/download",
				DiskPath:                 "v1/images/publish-flow/versions/v1.0.0/artifacts/" + artifact.ID.String() + "/download",
				MetadataSHA256:           digestHex(metadataDigest),
				MetadataSizeBytes:        512,
				DiskSHA256:               digestHex(diskDigest),
				DiskSizeBytes:            4096,
				CombinedDiskKVMImgSHA256: digestHex(catalogDigest("c")),
			},
		},
	})
	require.NoError(t, err)
	rows, err := store.IncusProjection().ListProjectionRows(ctx)
	require.NoError(t, err)
	assert.Empty(t, rows)

	finalizeStep := claimStep(t, ctx, publishStore, publishdomain.StepFinalizePublish)
	finalJob, err := publishStore.FinalizePublishStep(ctx, succeedStepParams(finalizeStep))
	require.NoError(t, err)
	assert.Equal(t, publishdomain.JobStateSucceeded, finalJob.State)

	manifest, err = catalogStore.GetVersionManifest(ctx, catalogdomain.GetVersionManifestParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, catalogdomain.VersionStatePublished, manifest.Version.State)
	assert.NotNil(t, manifest.Version.PublishedAt)
	rows, err = store.IncusProjection().ListProjectionRows(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, artifact.ID, rows[0].ArtifactID)
	assert.Equal(t, attachment.ID, rows[0].MetadataAttachmentID)
}

func TestPublishStepLeasePreventsStaleWorkerCompletion(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()
	publishStore := store.Publish()

	image, err := catalogStore.CreateImage(ctx, catalogdomain.CreateImageParams{Name: "publish-lease"})
	require.NoError(t, err)
	version, err := catalogStore.CreateDraftVersion(ctx, catalogdomain.CreateDraftVersionParams{
		ImageName: image.Name,
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	diskDigest := publishedCatalogDigest(image.ID, version.Version+":disk")
	insertTrustedBlob(t, store, diskDigest, 4096)
	_, err = catalogStore.AddArtifact(ctx, catalogdomain.AddArtifactParams{
		ImageName:            image.Name,
		Version:              version.Version,
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               catalogdomain.ArtifactFormatRaw,
		PrimaryBlobDigest:    diskDigest,
		PrimaryBlobSizeBytes: 4096,
		PrimaryMediaType:     "application/octet-stream",
	})
	require.NoError(t, err)
	_, err = publishStore.EnqueueVersion(ctx, publishdomain.EnqueueVersionParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)

	original := claimStepAs(t, ctx, publishStore, "old-worker", publishdomain.StepValidateCatalog)
	_, err = store.pool.Exec(
		ctx,
		`UPDATE publish_job_steps
		SET locked_at = now() - interval '10 minutes'
		WHERE id = $1`,
		original.ID,
	)
	require.NoError(t, err)
	reclaimed := claimStepAs(t, ctx, publishStore, "new-worker", publishdomain.StepValidateCatalog)
	require.Equal(t, original.ID, reclaimed.ID)
	assert.Equal(t, original.AttemptCount+1, reclaimed.AttemptCount)

	_, err = publishStore.SucceedValidateCatalogStep(ctx, publishdomain.SucceedStepParams{
		ID:           original.ID,
		WorkerID:     "old-worker",
		AttemptCount: original.AttemptCount,
	})
	assert.ErrorIs(t, err, publishdomain.ErrLeaseLost)
	_, err = publishStore.FailStep(ctx, publishdomain.FailStepParams{
		ID:             original.ID,
		WorkerID:       "old-worker",
		AttemptCount:   original.AttemptCount,
		FailureMessage: "stale failure",
	})
	assert.ErrorIs(t, err, publishdomain.ErrLeaseLost)

	_, err = publishStore.SucceedValidateCatalogStep(ctx, publishdomain.SucceedStepParams{
		ID:           reclaimed.ID,
		WorkerID:     "new-worker",
		AttemptCount: reclaimed.AttemptCount,
	})
	require.NoError(t, err)
}

func TestIncusProjectionRowsRequireCatalogOwnership(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()
	publishStore := store.Publish()

	image, err := catalogStore.CreateImage(ctx, catalogdomain.CreateImageParams{Name: "incus-ownership"})
	require.NoError(t, err)
	version, artifact, attachment, diskDigest, metadataDigest := createIncusArtifactFixture(
		t,
		ctx,
		store,
		catalogStore,
		image.Name,
		"v1.0.0",
	)
	_, otherArtifact, otherAttachment, otherDiskDigest, otherMetadataDigest := createIncusArtifactFixture(
		t,
		ctx,
		store,
		catalogStore,
		image.Name,
		"v2.0.0",
	)

	_, err = publishStore.EnqueueVersion(ctx, publishdomain.EnqueueVersionParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)
	validateStep := claimStep(t, ctx, publishStore, publishdomain.StepValidateCatalog)
	_, err = publishStore.SucceedValidateCatalogStep(ctx, succeedStepParams(validateStep))
	require.NoError(t, err)
	incusStep := claimStep(t, ctx, publishStore, publishdomain.StepIncusIndex)

	_, err = publishStore.SucceedIncusIndexStep(ctx, publishdomain.SucceedIncusIndexStepParams{
		ID:           incusStep.ID,
		WorkerID:     testPublishWorkerID,
		AttemptCount: incusStep.AttemptCount,
		VersionID:    version.ID,
		Rows: []incus.ProjectionRow{
			projectionRowFixture(
				version.ID,
				otherArtifact.ID,
				otherAttachment.ID,
				otherDiskDigest,
				otherMetadataDigest,
			),
		},
	})
	assert.ErrorIs(t, err, publishdomain.ErrNotFound)

	_, err = publishStore.SucceedIncusIndexStep(ctx, publishdomain.SucceedIncusIndexStepParams{
		ID:           incusStep.ID,
		WorkerID:     testPublishWorkerID,
		AttemptCount: incusStep.AttemptCount,
		VersionID:    version.ID,
		Rows: []incus.ProjectionRow{
			projectionRowFixture(version.ID, artifact.ID, otherAttachment.ID, diskDigest, otherMetadataDigest),
		},
	})
	assert.ErrorIs(t, err, publishdomain.ErrNotFound)

	_, err = publishStore.SucceedIncusIndexStep(ctx, publishdomain.SucceedIncusIndexStepParams{
		ID:           incusStep.ID,
		WorkerID:     testPublishWorkerID,
		AttemptCount: incusStep.AttemptCount,
		VersionID:    version.ID,
		Rows: []incus.ProjectionRow{
			projectionRowFixture(version.ID, artifact.ID, attachment.ID, diskDigest, metadataDigest),
		},
	})
	require.NoError(t, err)
}

func claimStep(
	t testing.TB,
	ctx context.Context,
	publishStore publishdomain.Store,
	name string,
) publishdomain.Step {
	t.Helper()

	step, err := publishStore.ClaimStep(ctx, publishdomain.ClaimStepParams{
		WorkerID:           testPublishWorkerID,
		StaleRunningBefore: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, name, step.Name)
	assert.Equal(t, publishdomain.StepStateRunning, step.State)

	return step
}

func createIncusArtifactFixture(
	t *testing.T,
	ctx context.Context,
	store *Store,
	catalogStore catalogdomain.Store,
	imageName string,
	versionName string,
) (catalogdomain.Version, catalogdomain.Artifact, catalogdomain.Attachment, catalogdomain.Digest, catalogdomain.Digest) {
	t.Helper()

	version, err := catalogStore.CreateDraftVersion(ctx, catalogdomain.CreateDraftVersionParams{
		ImageName: imageName,
		Version:   versionName,
	})
	require.NoError(t, err)
	diskDigest := publishedCatalogDigest(version.ID, "disk")
	metadataDigest := publishedCatalogDigest(version.ID, "metadata")
	insertTrustedBlob(t, store, diskDigest, 4096)
	insertTrustedBlob(t, store, metadataDigest, 512)
	artifact, err := catalogStore.AddArtifact(ctx, catalogdomain.AddArtifactParams{
		ImageName:            imageName,
		Version:              version.Version,
		OperatingSystem:      "linux",
		Architecture:         "x86_64",
		Format:               catalogdomain.ArtifactFormatQCOW2,
		PrimaryBlobDigest:    diskDigest,
		PrimaryBlobSizeBytes: 4096,
		PrimaryMediaType:     "application/x-qcow2",
	})
	require.NoError(t, err)
	attachment, err := catalogStore.AddAttachment(ctx, catalogdomain.AddAttachmentParams{
		ImageName:     imageName,
		Version:       version.Version,
		ArtifactID:    artifact.ID,
		Name:          "incus.tar.xz",
		MediaType:     "application/x-xz",
		BlobDigest:    metadataDigest,
		BlobSizeBytes: 512,
	})
	require.NoError(t, err)

	return version, artifact, attachment, diskDigest, metadataDigest
}

func projectionRowFixture(
	versionID uuid.UUID,
	artifactID uuid.UUID,
	attachmentID uuid.UUID,
	diskDigest catalogdomain.Digest,
	metadataDigest catalogdomain.Digest,
) incus.ProjectionRow {
	return incus.ProjectionRow{
		VersionID:                versionID,
		ArtifactID:               artifactID,
		MetadataAttachmentID:     attachmentID,
		MetadataPath:             "v1/metadata",
		DiskPath:                 "v1/disk",
		MetadataSHA256:           digestHex(metadataDigest),
		MetadataSizeBytes:        512,
		DiskSHA256:               digestHex(diskDigest),
		DiskSizeBytes:            4096,
		CombinedDiskKVMImgSHA256: digestHex(catalogDigest("c")),
	}
}

func claimStepAs(
	t testing.TB,
	ctx context.Context,
	publishStore publishdomain.Store,
	workerID string,
	name string,
) publishdomain.Step {
	t.Helper()

	step, err := publishStore.ClaimStep(ctx, publishdomain.ClaimStepParams{
		WorkerID:           workerID,
		StaleRunningBefore: time.Now().Add(-time.Minute),
	})
	require.NoError(t, err)
	assert.Equal(t, name, step.Name)
	assert.Equal(t, publishdomain.StepStateRunning, step.State)

	return step
}

func succeedStepParams(step publishdomain.Step) publishdomain.SucceedStepParams {
	return publishdomain.SucceedStepParams{
		ID:           step.ID,
		WorkerID:     testPublishWorkerID,
		AttemptCount: step.AttemptCount,
	}
}

const testPublishWorkerID = "publish-test"

func digestHex(digest catalogdomain.Digest) string {
	return digest.String()[len("sha256:"):]
}
