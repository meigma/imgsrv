package uploads

import (
	"fmt"

	"github.com/google/uuid"

	domain "github.com/meigma/imgsrv/internal/uploads"
)

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

func validateGetSessionParams(params domain.GetSessionParams) error {
	return validateUploadID(params.ID)
}

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

func validateCompleteSessionParams(params domain.CompleteSessionParams) error {
	return validateUploadID(params.ID)
}

func validateAbortSessionParams(params domain.AbortSessionParams) error {
	return validateUploadID(params.ID)
}

func validateClaimIngestJobParams(params domain.ClaimIngestJobParams) error {
	return domain.ValidateRequiredText("worker id", params.WorkerID)
}

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

func validateFailIngestJobParams(params domain.FailIngestJobParams) error {
	if err := validateIngestJobID(params.ID); err != nil {
		return err
	}

	return domain.ValidateRequiredText("failure message", params.FailureMessage)
}

func validateUploadID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: upload id is required", domain.ErrInvalid)
	}

	return nil
}

func validateIngestJobID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: ingest job id is required", domain.ErrInvalid)
	}

	return nil
}
