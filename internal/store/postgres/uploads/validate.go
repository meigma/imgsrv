package uploads

import (
	"fmt"

	"github.com/google/uuid"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

// validateCreateSessionParams checks that params describe a well-formed
// upload session before it is persisted.
func validateCreateSessionParams(params domain.CreateSessionParams) error {
	if err := validateUploadID(params.ID); err != nil {
		return err
	}
	if err := domain.ValidateDigest(params.ExpectedDigest); err != nil {
		return err
	}
	if err := domain.ValidateNonNegativeSize("expected size", params.ExpectedSizeBytes); err != nil {
		return err
	}
	if err := domain.ValidateRequiredText("storage upload id", params.StorageUploadID); err != nil {
		return err
	}
	if err := domain.ValidateOptionalText("media type hint", params.MediaTypeHint); err != nil {
		return err
	}
	if err := domain.ValidateOptionalText("filename hint", params.FilenameHint); err != nil {
		return err
	}
	if params.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires at is required", domain.ErrInvalid)
	}

	return nil
}

// validateCreateReadySessionParams checks that params describe a well-formed
// terminal ready upload session before it is persisted.
func validateCreateReadySessionParams(params domain.CreateReadySessionParams) error {
	if err := validateUploadID(params.ID); err != nil {
		return err
	}
	if err := domain.ValidateDigest(params.ExpectedDigest); err != nil {
		return err
	}
	if err := domain.ValidateNonNegativeSize("expected size", params.ExpectedSizeBytes); err != nil {
		return err
	}
	if err := domain.ValidateOptionalText("media type hint", params.MediaTypeHint); err != nil {
		return err
	}
	if err := domain.ValidateOptionalText("filename hint", params.FilenameHint); err != nil {
		return err
	}
	if params.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires at is required", domain.ErrInvalid)
	}

	return nil
}

// validateGetSessionParams checks that params identify an upload session.
func validateGetSessionParams(params domain.GetSessionParams) error {
	return validateUploadID(params.ID)
}

// validateGetTrustedBlobParams checks that params identify a trusted CAS blob.
func validateGetTrustedBlobParams(params domain.GetTrustedBlobParams) error {
	return domain.ValidateDigest(params.Digest)
}

// validatePutPartParams checks that params describe a well-formed multipart
// part record before it is persisted.
func validatePutPartParams(params domain.PutPartParams) error {
	if err := validateUploadID(params.UploadID); err != nil {
		return err
	}
	if err := domain.ValidatePartNumber(params.PartNumber); err != nil {
		return err
	}
	if err := domain.ValidateRequiredText("etag", params.ETag); err != nil {
		return err
	}

	return domain.ValidateNonNegativeSize("part size", params.SizeBytes)
}

// validateCompleteSessionParams checks that params identify an upload session.
func validateCompleteSessionParams(params domain.CompleteSessionParams) error {
	return validateUploadID(params.ID)
}

// validateAbortSessionParams checks that params identify an upload session.
func validateAbortSessionParams(params domain.AbortSessionParams) error {
	return validateUploadID(params.ID)
}

// validateClaimIngestJobParams checks that params identify the worker
// claiming an ingest job.
func validateClaimIngestJobParams(params domain.ClaimIngestJobParams) error {
	return domain.ValidateRequiredText("worker id", params.WorkerID)
}

// validateSucceedIngestJobParams checks that params describe a verified CAS
// blob outcome before it is persisted.
func validateSucceedIngestJobParams(params domain.SucceedIngestJobParams) error {
	if err := validateIngestJobID(params.ID); err != nil {
		return err
	}
	if err := domain.ValidateDigest(params.Digest); err != nil {
		return err
	}
	if err := domain.ValidateNonNegativeSize("blob size", params.SizeBytes); err != nil {
		return err
	}
	if err := domain.ValidateRequiredText("storage key", params.StorageKey); err != nil {
		return err
	}

	return domain.ValidateOptionalText("media type", params.MediaType)
}

// validateFailIngestJobParams checks that params describe a terminal ingest
// failure before it is persisted.
func validateFailIngestJobParams(params domain.FailIngestJobParams) error {
	if err := validateIngestJobID(params.ID); err != nil {
		return err
	}

	return domain.ValidateRequiredText("failure message", params.FailureMessage)
}

// validateUploadID returns ErrInvalid when id is the zero UUID.
func validateUploadID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: upload id is required", domain.ErrInvalid)
	}

	return nil
}

// validateIngestJobID returns ErrInvalid when id is the zero UUID.
func validateIngestJobID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: ingest job id is required", domain.ErrInvalid)
	}

	return nil
}
