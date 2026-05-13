// Package promote claims completed uploads and promotes them into CAS.
package promote

import (
	"context"
	"errors"
	"log/slog"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/jobs"
	safelog "github.com/meigma/imgsrv/internal/logging"
	"github.com/meigma/imgsrv/internal/uploads"
)

// UploadStore provides the upload state needed by CAS promotion.
type UploadStore interface {
	// ClaimIngestJob claims the next queued CAS ingest job for a worker.
	ClaimIngestJob(context.Context, uploads.ClaimIngestJobParams) (uploads.IngestJob, error)

	// GetSession returns durable upload state by upload ID.
	GetSession(context.Context, uploads.GetSessionParams) (uploads.Session, error)
}

// Committer commits a running CAS ingest job into trusted CAS state.
type Committer interface {
	// CommitStagedUpload verifies staged bytes and commits them into CAS.
	CommitStagedUpload(context.Context, cas.CommitStagedUploadParams) (cas.CommitStagedUploadResult, error)
}

// Config configures a CAS promotion job.
type Config struct {
	// Uploads claims CAS ingest jobs and loads their upload sessions.
	Uploads UploadStore

	// CAS commits verified staged uploads into content-addressed storage.
	CAS Committer

	// Logger receives CAS promotion logs. Nil selects a discarded logger.
	Logger *slog.Logger
}

// Job claims one completed upload and promotes it into CAS.
type Job struct {
	// uploads claims CAS ingest jobs and loads their upload sessions.
	uploads UploadStore
	// cas commits verified staged uploads into content-addressed storage.
	cas Committer
	// logger receives CAS promotion logs.
	logger *slog.Logger
}

// New constructs a CAS promotion job from config.
func New(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = safelog.Nop()
	}

	return &Job{
		uploads: config.Uploads,
		cas:     config.CAS,
		logger:  logger,
	}
}

// RunOnce claims and promotes at most one queued CAS ingest job.
func (job *Job) RunOnce(ctx context.Context, workerID string) (jobs.Result, error) {
	uploadStore, committer, err := job.dependencies()
	if err != nil {
		return jobs.Result{}, err
	}
	if validationErr := uploads.ValidateRequiredText("worker id", workerID); validationErr != nil {
		return jobs.Result{}, validationErr
	}

	ingestJob, err := uploadStore.ClaimIngestJob(ctx, uploads.ClaimIngestJobParams{
		WorkerID: workerID,
	})
	if err != nil {
		if errors.Is(err, uploads.ErrNotFound) {
			return jobs.Result{}, nil
		}

		return jobs.Result{}, err
	}
	attrs := ingestJobAttrs(ingestJob)
	job.logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"cas promotion job claimed",
		append(attrs, slog.String("operation", "cas_promotion.claim"))...)

	session, err := uploadStore.GetSession(ctx, uploads.GetSessionParams{ID: ingestJob.UploadID})
	if err != nil {
		return jobs.Result{Attrs: attrs}, err
	}
	attrs = append(attrs,
		slog.String("upload_id", session.ID.String()),
		slog.String("expected_digest", session.ExpectedDigest.String()),
		slog.Int64("expected_size_bytes", session.ExpectedSizeBytes),
	)

	_, err = committer.CommitStagedUpload(ctx, cas.CommitStagedUploadParams{
		JobID:             ingestJob.ID,
		UploadID:          session.ID,
		StagingKey:        session.StagingKey,
		ExpectedDigest:    session.ExpectedDigest,
		ExpectedSizeBytes: session.ExpectedSizeBytes,
		MediaType:         session.MediaTypeHint,
	})
	if err != nil {
		return jobs.Result{Attrs: attrs}, err
	}
	job.logger.LogAttrs(
		ctx,
		slog.LevelInfo,
		"cas promotion job succeeded",
		append(attrs, slog.String("operation", "cas_promotion.succeed"))...)

	return jobs.Result{Worked: true, Attrs: attrs}, nil
}

func ingestJobAttrs(ingestJob uploads.IngestJob) []slog.Attr {
	return []slog.Attr{
		slog.String("ingest_job_id", ingestJob.ID.String()),
		slog.String("upload_id", ingestJob.UploadID.String()),
		slog.Int("attempt_count", ingestJob.AttemptCount),
		slog.String("state", string(ingestJob.State)),
	}
}

// dependencies returns the configured upload store and CAS committer or an error when the job is not usable.
func (job *Job) dependencies() (UploadStore, Committer, error) {
	if job == nil {
		return nil, nil, errors.New("cas promotion job is not configured")
	}
	if job.uploads == nil {
		return nil, nil, errors.New("upload store is required")
	}
	if job.cas == nil {
		return nil, nil, errors.New("cas committer is required")
	}

	return job.uploads, job.cas, nil
}
