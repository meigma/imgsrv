package catalog

import (
	"fmt"

	"github.com/google/uuid"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

// validateCreateImageParams validates the inputs to Store.CreateImage.
func validateCreateImageParams(params domain.CreateImageParams) error {
	if err := domain.ValidateImageName(params.Name); err != nil {
		return err
	}
	if err := validateOptionalText("display name", params.DisplayName); err != nil {
		return err
	}

	return validateOptionalText("description", params.Description)
}

// validateListImagesParams validates the inputs to Store.ListImages.
func validateListImagesParams(domain.ListImagesParams) error {
	return nil
}

// validateGetImageParams validates the inputs to Store.GetImage.
func validateGetImageParams(params domain.GetImageParams) error {
	return domain.ValidateImageName(params.Name)
}

// validateCreateDraftVersionParams validates the inputs to
// Store.CreateDraftVersion.
func validateCreateDraftVersionParams(params domain.CreateDraftVersionParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}

	return domain.ValidateVersion(params.Version)
}

// validateListVersionsParams validates the inputs to Store.ListVersions.
func validateListVersionsParams(params domain.ListVersionsParams) error {
	return domain.ValidateImageName(params.ImageName)
}

// validateAddArtifactParams validates the inputs to Store.AddArtifact.
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

// validateAddAttachmentParams validates the inputs to Store.AddAttachment.
func validateAddAttachmentParams(params domain.AddAttachmentParams) error {
	if err := validateCreateDraftVersionParams(domain.CreateDraftVersionParams{
		ImageName: params.ImageName,
		Version:   params.Version,
	}); err != nil {
		return err
	}
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

// validateDeleteArtifactParams validates the inputs to Store.DeleteArtifact.
func validateDeleteArtifactParams(params domain.DeleteArtifactParams) error {
	if err := validateCreateDraftVersionParams(domain.CreateDraftVersionParams{
		ImageName: params.ImageName,
		Version:   params.Version,
	}); err != nil {
		return err
	}
	if params.ArtifactID == uuid.Nil {
		return fmt.Errorf("%w: artifact id is required", domain.ErrInvalid)
	}

	return nil
}

// validateDeleteAttachmentParams validates the inputs to Store.DeleteAttachment.
func validateDeleteAttachmentParams(params domain.DeleteAttachmentParams) error {
	if err := validateDeleteArtifactParams(domain.DeleteArtifactParams{
		ImageName:  params.ImageName,
		Version:    params.Version,
		ArtifactID: params.ArtifactID,
	}); err != nil {
		return err
	}
	if params.AttachmentID == uuid.Nil {
		return fmt.Errorf("%w: attachment id is required", domain.ErrInvalid)
	}

	return nil
}

// validatePublishVersionParams validates the inputs to Store.PublishVersion.
func validatePublishVersionParams(params domain.PublishVersionParams) error {
	return validateCreateDraftVersionParams(domain.CreateDraftVersionParams(params))
}

// validatePutAliasParams validates the inputs to Store.PutAlias.
func validatePutAliasParams(params domain.PutAliasParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}
	if err := domain.ValidateAlias(params.Alias); err != nil {
		return err
	}

	return domain.ValidateVersion(params.Version)
}

// validateListAliasesParams validates inputs to Store.ListAliases.
func validateListAliasesParams(params domain.ListAliasesParams) error {
	return domain.ValidateImageName(params.ImageName)
}

// validateGetAliasParams validates the inputs to Store.GetAlias.
func validateGetAliasParams(params domain.GetAliasParams) error {
	if err := domain.ValidateImageName(params.ImageName); err != nil {
		return err
	}

	return domain.ValidateAlias(params.Alias)
}

// validateDeleteAliasParams validates inputs to Store.DeleteAlias.
func validateDeleteAliasParams(params domain.DeleteAliasParams) error {
	return validateGetAliasParams(domain.GetAliasParams(params))
}

// validateGetVersionManifestParams validates inputs to Store.GetVersionManifest.
func validateGetVersionManifestParams(params domain.GetVersionManifestParams) error {
	return validateCreateDraftVersionParams(domain.CreateDraftVersionParams(params))
}

// validateResolveManifestParams validates the inputs to Store.ResolveManifest.
func validateResolveManifestParams(params domain.ResolveManifestParams) error {
	return validateCreateDraftVersionParams(domain.CreateDraftVersionParams(params))
}

// validateOptionalText accepts nil and otherwise enforces the required-text
// rule for the named field.
func validateOptionalText(field string, value *string) error {
	if value == nil {
		return nil
	}

	return domain.ValidateRequiredText(field, *value)
}

// validateDigest validates that digest matches the catalog sha256 digest form.
func validateDigest(digest domain.Digest) error {
	_, err := domain.ParseDigest(digest.String())
	return err
}
