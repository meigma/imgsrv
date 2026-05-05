package client

// Digest identifies an expected or verified content blob.
type Digest string

// String returns the digest string.
func (digest Digest) String() string {
	return string(digest)
}

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
