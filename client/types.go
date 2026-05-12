package client

// Digest identifies an expected or verified content blob.
type Digest string

// String returns the digest string.
func (digest Digest) String() string {
	return string(digest)
}

// ArtifactID identifies a release artifact.
type ArtifactID string

// String returns the artifact ID string.
func (id ArtifactID) String() string {
	return string(id)
}

// ArtifactFormat is a supported primary artifact format.
type ArtifactFormat string

const (
	// ArtifactFormatRaw is a raw disk image artifact.
	ArtifactFormatRaw ArtifactFormat = "raw"

	// ArtifactFormatRawGZ is a gzip-compressed raw disk image artifact.
	ArtifactFormatRawGZ ArtifactFormat = "raw.gz"

	// ArtifactFormatQCOW2 is a qcow2 disk image artifact.
	ArtifactFormatQCOW2 ArtifactFormat = "qcow2"
)

// ImageVersionState is the lifecycle state for an image version.
type ImageVersionState string

const (
	// ImageVersionStateDraft means a version manifest can still be edited.
	ImageVersionStateDraft ImageVersionState = "draft"

	// ImageVersionStatePublishing means a version manifest is frozen while publish steps run.
	ImageVersionStatePublishing ImageVersionState = "publishing"

	// ImageVersionStatePublished means a version manifest is immutable.
	ImageVersionStatePublished ImageVersionState = "published"
)

// PublishJobID identifies a durable publish job.
type PublishJobID string

// String returns the publish job ID string.
func (id PublishJobID) String() string {
	return string(id)
}

// PublishJobState is the lifecycle state of a durable publish job.
type PublishJobState string

const (
	// PublishJobStateQueued means no publish step has started yet.
	PublishJobStateQueued PublishJobState = "queued"

	// PublishJobStateRunning means at least one publish step is running or has run.
	PublishJobStateRunning PublishJobState = "running"

	// PublishJobStateSucceeded means the version is published.
	PublishJobStateSucceeded PublishJobState = "succeeded"

	// PublishJobStateFailed means a blocking publish step failed.
	PublishJobStateFailed PublishJobState = "failed"
)

// PublishStepState is the lifecycle state of one durable publish step.
type PublishStepState string

const (
	// PublishStepStateQueued means the step is waiting to be claimed.
	PublishStepStateQueued PublishStepState = "queued"

	// PublishStepStateRunning means a worker has claimed the step.
	PublishStepStateRunning PublishStepState = "running"

	// PublishStepStateSucceeded means the step completed successfully.
	PublishStepStateSucceeded PublishStepState = "succeeded"

	// PublishStepStateFailed means the step failed.
	PublishStepStateFailed PublishStepState = "failed"

	// PublishStepStateSkipped means the step was intentionally skipped.
	PublishStepStateSkipped PublishStepState = "skipped"
)

// UploadID identifies an upload session.
type UploadID string

// String returns the upload ID string.
func (id UploadID) String() string {
	return string(id)
}

// UploadState is the durable upload-session lifecycle state.
type UploadState string

const (
	// UploadStateCreated means the upload session exists but has no accepted parts yet.
	UploadStateCreated UploadState = "created"

	// UploadStateUploading means at least one part has been accepted.
	UploadStateUploading UploadState = "uploading"

	// UploadStateCompleted means object storage has completed the multipart object.
	UploadStateCompleted UploadState = "completed"

	// UploadStateIngesting means a worker is verifying and promoting the staged object.
	UploadStateIngesting UploadState = "ingesting"

	// UploadStateReady means the expected digest exists as a verified CAS blob.
	UploadStateReady UploadState = "ready"

	// UploadStateFailed means ingest failed.
	UploadStateFailed UploadState = "failed"

	// UploadStateAborted means the upload was explicitly aborted before ingest.
	UploadStateAborted UploadState = "aborted"
)

// String returns the upload state string.
func (state UploadState) String() string {
	return string(state)
}
