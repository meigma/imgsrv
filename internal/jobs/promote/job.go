// Package promote claims completed uploads and promotes them into CAS.
package promote

import (
	"context"
	"errors"

	"github.com/meigma/imgsrv/internal/cas"
	"github.com/meigma/imgsrv/internal/jobs"
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
}

// Job claims one completed upload and promotes it into CAS.
type Job struct {
	uploads UploadStore
	cas     Committer
}

// New constructs a CAS promotion job from config.
func New(config Config) *Job {
	return &Job{
		uploads: config.Uploads,
		cas:     config.CAS,
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

	session, err := uploadStore.GetSession(ctx, uploads.GetSessionParams{ID: ingestJob.UploadID})
	if err != nil {
		return jobs.Result{}, err
	}

	_, err = committer.CommitStagedUpload(ctx, cas.CommitStagedUploadParams{
		JobID:             ingestJob.ID,
		UploadID:          session.ID,
		StagingKey:        session.StagingKey,
		ExpectedDigest:    session.ExpectedDigest,
		ExpectedSizeBytes: session.ExpectedSizeBytes,
		MediaType:         session.MediaTypeHint,
	})
	if err != nil {
		return jobs.Result{}, err
	}

	return jobs.Result{Worked: true}, nil
}

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
