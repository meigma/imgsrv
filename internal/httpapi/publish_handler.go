package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/imgsrv/internal/publish"
)

// errPublishServiceUnavailable signals that publish routes were called without a configured PublishService.
var errPublishServiceUnavailable = errors.New("publish service is not configured")

// publishJobResponse is the JSON wire shape of a durable publish job.
type publishJobResponse struct {
	// ID is the stable publish-job identity.
	ID string `json:"id"`

	// VersionID identifies the image version being published.
	VersionID string `json:"version_id"`

	// ImageName is the image namespace for the version being published.
	ImageName string `json:"image_name"`

	// Version is the operator-defined version string being published.
	Version string `json:"version"`

	// State is the durable publish-job lifecycle state.
	State publish.JobState `json:"state"`

	// StartedAt is set when a worker first claims a step.
	StartedAt *string `json:"started_at,omitempty"`

	// FinishedAt is set when the job reaches a terminal state.
	FinishedAt *string `json:"finished_at,omitempty"`

	// FailureMessage describes the blocking failure when State is failed.
	FailureMessage *string `json:"failure_message,omitempty"`

	// CreatedAt is when the job was queued.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when the job last changed.
	UpdatedAt string `json:"updated_at"`

	// Steps are the durable units of publish progress in execution order.
	Steps []publishJobStepResponse `json:"steps"`
}

// publishJobStepResponse is the JSON wire shape of one publish job step.
type publishJobStepResponse struct {
	// ID is the stable publish-step identity.
	ID string `json:"id"`

	// JobID identifies the parent publish job.
	JobID string `json:"job_id"`

	// Name identifies the publish step handler.
	Name string `json:"name"`

	// State is the durable publish-step lifecycle state.
	State publish.StepState `json:"state"`

	// Blocking controls whether failure blocks later publish steps.
	Blocking bool `json:"blocking"`

	// Sequence orders steps within the parent job.
	Sequence int `json:"sequence"`

	// AttemptCount counts durable claims of this step.
	AttemptCount int `json:"attempt_count"`

	// StartedAt is set when the step is first claimed.
	StartedAt *string `json:"started_at,omitempty"`

	// FinishedAt is set when the step reaches a terminal state.
	FinishedAt *string `json:"finished_at,omitempty"`

	// FailureMessage describes the failure when State is failed.
	FailureMessage *string `json:"failure_message,omitempty"`

	// CreatedAt is when the step was queued.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is when the step last changed.
	UpdatedAt string `json:"updated_at"`
}

// getPublishJob handles GET /v1/publish-jobs/{job_id}.
func (a *api) getPublishJob(w http.ResponseWriter, r *http.Request) {
	service, ok := a.publishService(w)
	if !ok {
		return
	}

	jobID, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "publish job id must be a UUID")
		return
	}

	job, err := service.GetPublishJob(r.Context(), publish.GetJobParams{ID: jobID})
	if err != nil {
		writePublishError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newPublishJobResponse(job))
}

// retryPublishJob handles POST /v1/publish-jobs/{job_id}/retry.
func (a *api) retryPublishJob(w http.ResponseWriter, r *http.Request) {
	service, ok := a.publishService(w)
	if !ok {
		return
	}

	jobID, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "publish job id must be a UUID")
		return
	}

	job, err := service.RetryPublishJob(r.Context(), publish.RetryJobParams{ID: jobID})
	if err != nil {
		writePublishError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, newPublishJobResponse(job))
}

// publishService returns the configured PublishService or writes a 503 problem and reports false.
func (a *api) publishService(w http.ResponseWriter) (PublishService, bool) {
	if a.publish == nil {
		writeProblem(w, http.StatusServiceUnavailable, errPublishServiceUnavailable.Error())
		return nil, false
	}

	return a.publish, true
}

func newPublishJobResponse(job publish.Job) publishJobResponse {
	response := publishJobResponse{
		ID:             job.ID.String(),
		VersionID:      job.VersionID.String(),
		ImageName:      job.ImageName,
		Version:        job.Version,
		State:          job.State,
		StartedAt:      optionalHTTPTime(job.StartedAt),
		FinishedAt:     optionalHTTPTime(job.FinishedAt),
		FailureMessage: job.FailureMessage,
		CreatedAt:      formatCatalogTime(job.CreatedAt),
		UpdatedAt:      formatCatalogTime(job.UpdatedAt),
		Steps:          make([]publishJobStepResponse, 0, len(job.Steps)),
	}
	for _, step := range job.Steps {
		response.Steps = append(response.Steps, publishJobStepResponse{
			ID:             step.ID.String(),
			JobID:          step.JobID.String(),
			Name:           step.Name,
			State:          step.State,
			Blocking:       step.Blocking,
			Sequence:       step.Sequence,
			AttemptCount:   step.AttemptCount,
			StartedAt:      optionalHTTPTime(step.StartedAt),
			FinishedAt:     optionalHTTPTime(step.FinishedAt),
			FailureMessage: step.FailureMessage,
			CreatedAt:      formatCatalogTime(step.CreatedAt),
			UpdatedAt:      formatCatalogTime(step.UpdatedAt),
		})
	}

	return response
}

func optionalHTTPTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatCatalogTime(*value)

	return &formatted
}
