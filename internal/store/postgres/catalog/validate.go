package catalog

import (
	"fmt"

	"github.com/google/uuid"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

func validateCreateImageParams(params domain.CreateImageParams) error {
	if err := domain.ValidateImageName(params.Name); err != nil {
		return err
	}
	if err := validateOptionalText("display name", params.DisplayName); err != nil {
		return err
	}

	return validateOptionalText("description", params.Description)
}

func validateCreateDraftVersionParams(params domain.CreateDraftVersionParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}

	return domain.ValidateVersion(params.Version)
}

func validateAddArtifactParams(params domain.AddArtifactParams) error {
	if err := validateCreateDraftVersionParams(domain.CreateDraftVersionParams{
		ImageName: params.ImageName,
		Version:   params.Version,
	}); err != nil {
		return err
	}
	if err := domain.ValidateToken("operating system", params.OperatingSystem); err != nil {
		return err
	}
	if err := domain.ValidateToken("architecture", params.Architecture); err != nil {
		return err
	}
	if err := domain.ValidateArtifactFormat(params.Format); err != nil {
		return err
	}
	if err := validateDigest(params.PrimaryBlobDigest); err != nil {
		return err
	}
	if err := domain.ValidateNonNegativeSize("primary blob size", params.PrimaryBlobSizeBytes); err != nil {
		return err
	}

	return domain.ValidateRequiredText("primary media type", params.PrimaryMediaType)
}

func validateAddAttachmentParams(params domain.AddAttachmentParams) error {
	if params.ArtifactID == uuid.Nil {
		return fmt.Errorf("%w: artifact id is required", domain.ErrInvalid)
	}
	if err := domain.ValidateToken("attachment name", params.Name); err != nil {
		return err
	}
	if err := domain.ValidateRequiredText("attachment media type", params.MediaType); err != nil {
		return err
	}
	if err := validateDigest(params.BlobDigest); err != nil {
		return err
	}

	return domain.ValidateNonNegativeSize("attachment blob size", params.BlobSizeBytes)
}

func validatePublishVersionParams(params domain.PublishVersionParams) error {
	return validateCreateDraftVersionParams(domain.CreateDraftVersionParams(params))
}

func validatePutAliasParams(params domain.PutAliasParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}
	if err := domain.ValidateAlias(params.Alias); err != nil {
		return err
	}

	return domain.ValidateVersion(params.Version)
}

func validateGetAliasParams(params domain.GetAliasParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}

	return domain.ValidateAlias(params.Alias)
}

func validateResolveManifestParams(params domain.ResolveManifestParams) error {
	return validateCreateDraftVersionParams(domain.CreateDraftVersionParams(params))
}

func validateOptionalText(field string, value *string) error {
	if value == nil {
		return nil
	}

	return domain.ValidateRequiredText(field, *value)
}

func validateDigest(digest domain.Digest) error {
	_, err := domain.ParseDigest(digest.String())
	return err
}
