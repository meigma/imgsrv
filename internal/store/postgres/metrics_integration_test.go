//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogdomain "github.com/meigma/imgsrv/internal/catalog"
	"github.com/meigma/imgsrv/internal/metrics"
	publishdomain "github.com/meigma/imgsrv/internal/publish"
	uploadsdomain "github.com/meigma/imgsrv/internal/uploads"
)

func TestStoreMetricsSnapshotsDurableState(t *testing.T) {
	ctx := t.Context()
	store := startCatalogIntegrationStore(t)
	catalogStore := store.Catalog()
	publishStore := store.Publish()

	insertUploadMetricFixture(t, store, "completed", "queued", time.Now().Add(-3*time.Minute))
	insertUploadMetricFixture(t, store, "ingesting", "running", time.Now().Add(-5*time.Minute))

	image, err := catalogStore.CreateImage(ctx, catalogdomain.CreateImageParams{Name: "metrics-state"})
	require.NoError(t, err)
	version, err := catalogStore.CreateDraftVersion(ctx, catalogdomain.CreateDraftVersionParams{
		ImageName: image.Name,
		Version:   "v1.0.0",
	})
	require.NoError(t, err)
	diskDigest := catalogDigest("d")
	metadataDigest := catalogDigest("e")
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
	_, err = publishStore.EnqueueVersion(ctx, publishdomain.EnqueueVersionParams{
		ImageName: image.Name,
		Version:   version.Version,
	})
	require.NoError(t, err)
	validateStep := claimStep(t, ctx, publishStore, publishdomain.StepValidateCatalog)
	_, err = store.pool.Exec(
		ctx,
		`UPDATE publish_job_steps
		SET locked_at = now() - interval '2 minutes'
		WHERE id = $1`,
		validateStep.ID,
	)
	require.NoError(t, err)
	_, err = store.pool.Exec(
		ctx,
		`INSERT INTO incus_projection_items (
			artifact_id,
			version_id,
			metadata_attachment_id,
			metadata_path,
			disk_path,
			metadata_sha256,
			metadata_size_bytes,
			disk_sha256,
			disk_size_bytes,
			combined_disk_kvm_img_sha256
		)
		VALUES ($1, $2, $3, 'metadata.tar.xz', 'disk.qcow2', $4, 512, $5, 4096, $6)`,
		artifact.ID,
		version.ID,
		attachment.ID,
		digestHex(metadataDigest),
		digestHex(diskDigest),
		digestHex(catalogDigest("f")),
	)
	require.NoError(t, err)

	snapshot, err := store.StoreMetrics(ctx)

	require.NoError(t, err)
	assertStateCount(t, snapshot.UploadSessions, "completed", 1)
	assertStateCount(t, snapshot.UploadSessions, "ingesting", 1)
	assertStateCount(t, snapshot.CASIngestJobs, "queued", 1)
	assertStateCount(t, snapshot.CASIngestJobs, "running", 1)
	assert.True(t, snapshot.HasCASIngestOldestQueuedAge)
	assert.Positive(t, snapshot.CASIngestOldestQueuedAge)
	assert.True(t, snapshot.HasCASIngestOldestRunningAge)
	assert.Positive(t, snapshot.CASIngestOldestRunningAge)
	assert.Equal(t, int64(2), snapshot.CASBlobs)
	assert.Equal(t, int64(4608), snapshot.CASBlobBytes)
	assertStateCount(t, snapshot.PublishJobs, "running", 1)
	assertStepStateCount(t, snapshot.PublishSteps, publishdomain.StepValidateCatalog, "running", 1)
	assertStepStateCount(t, snapshot.PublishSteps, publishdomain.StepIncusIndex, "queued", 1)
	assertStepStateCount(t, snapshot.PublishSteps, publishdomain.StepFinalizePublish, "queued", 1)
	assertStepAge(t, snapshot.PublishStepOldestRunningAges, publishdomain.StepValidateCatalog)
	assert.Equal(t, int64(1), snapshot.PublishingVersions)
	assert.Equal(t, int64(1), snapshot.IncusProjectionRows)
}

func insertUploadMetricFixture(t *testing.T, store *Store, sessionState string, jobState string, referenceTime time.Time) {
	t.Helper()

	uploadID := uuid.New()
	digest := uploadsdomain.Digest(catalogDigest("a").String())
	_, err := store.pool.Exec(
		t.Context(),
		`INSERT INTO upload_sessions (
			id,
			expected_digest,
			expected_size_bytes,
			state,
			storage_upload_id,
			staging_key,
			completed_at,
			ingest_started_at,
			expires_at
		)
		VALUES ($1, $2, 1, $3, $4, $5, now() - interval '10 minutes', $6, now() + interval '1 hour')`,
		uploadID,
		digest,
		sessionState,
		"upload-"+uploadID.String(),
		"staging/uploads/"+uploadID.String(),
		nullableTime(sessionState == "ingesting", referenceTime),
	)
	require.NoError(t, err)

	var lockedBy any
	var lockedAt any
	var startedAt any
	if jobState == "running" {
		lockedBy = "metrics-worker"
		lockedAt = referenceTime
		startedAt = referenceTime
	}
	_, err = store.pool.Exec(
		t.Context(),
		`INSERT INTO cas_ingest_jobs (
			id,
			upload_id,
			state,
			run_after,
			locked_by,
			locked_at,
			started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(),
		uploadID,
		jobState,
		referenceTime,
		lockedBy,
		lockedAt,
		startedAt,
	)
	require.NoError(t, err)
}

func nullableTime(valid bool, value time.Time) any {
	if !valid {
		return nil
	}

	return value
}

func assertStateCount(t *testing.T, counts []metrics.StateCount, state string, want int64) {
	t.Helper()

	for _, count := range counts {
		if count.State == state {
			assert.Equal(t, want, count.Count)
			return
		}
	}
	assert.Failf(t, "missing state count", "state %q not found in %#v", state, counts)
}

func assertStepStateCount(t *testing.T, counts []metrics.StepStateCount, step string, state string, want int64) {
	t.Helper()

	for _, count := range counts {
		if count.Step == step && count.State == state {
			assert.Equal(t, want, count.Count)
			return
		}
	}
	assert.Failf(t, "missing step state count", "step %q state %q not found in %#v", step, state, counts)
}

func assertStepAge(t *testing.T, ages []metrics.StepAge, step string) {
	t.Helper()

	for _, age := range ages {
		if age.Step == step {
			assert.Positive(t, age.Age)
			return
		}
	}
	assert.Failf(t, "missing step age", "step %q not found in %#v", step, ages)
}
