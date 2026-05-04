package catalog

import (
	"database/sql"
	"time"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanImage(row rowScanner) (domain.Image, error) {
	var image domain.Image
	var displayName sql.NullString
	var description sql.NullString

	err := row.Scan(
		&image.ID,
		&image.Name,
		&displayName,
		&description,
		&image.CreatedAt,
		&image.UpdatedAt,
	)
	if err != nil {
		return domain.Image{}, err
	}

	image.DisplayName = optionalString(displayName)
	image.Description = optionalString(description)

	return image, nil
}

func scanVersion(row rowScanner) (domain.Version, error) {
	var version domain.Version
	var publishedAt sql.NullTime

	err := row.Scan(
		&version.ID,
		&version.ImageID,
		&version.Version,
		&version.State,
		&publishedAt,
		&version.CreatedAt,
		&version.UpdatedAt,
	)
	if err != nil {
		return domain.Version{}, err
	}

	version.PublishedAt = optionalTime(publishedAt)

	return version, nil
}

func scanImageAndVersion(row rowScanner) (domain.Image, domain.Version, error) {
	var image domain.Image
	var version domain.Version
	var displayName sql.NullString
	var description sql.NullString
	var publishedAt sql.NullTime

	err := row.Scan(
		&image.ID,
		&image.Name,
		&displayName,
		&description,
		&image.CreatedAt,
		&image.UpdatedAt,
		&version.ID,
		&version.ImageID,
		&version.Version,
		&version.State,
		&publishedAt,
		&version.CreatedAt,
		&version.UpdatedAt,
	)
	if err != nil {
		return domain.Image{}, domain.Version{}, err
	}

	image.DisplayName = optionalString(displayName)
	image.Description = optionalString(description)
	version.PublishedAt = optionalTime(publishedAt)

	return image, version, nil
}

func scanArtifact(row rowScanner) (domain.Artifact, error) {
	var artifact domain.Artifact

	err := row.Scan(
		&artifact.ID,
		&artifact.VersionID,
		&artifact.OperatingSystem,
		&artifact.Architecture,
		&artifact.Format,
		&artifact.PrimaryBlobDigest,
		&artifact.PrimaryBlobSizeBytes,
		&artifact.PrimaryMediaType,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	if err != nil {
		return domain.Artifact{}, err
	}

	return artifact, nil
}

func scanAttachment(row rowScanner) (domain.Attachment, error) {
	var attachment domain.Attachment

	err := row.Scan(
		&attachment.ID,
		&attachment.ArtifactID,
		&attachment.Name,
		&attachment.MediaType,
		&attachment.BlobDigest,
		&attachment.BlobSizeBytes,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
	)
	if err != nil {
		return domain.Attachment{}, err
	}

	return attachment, nil
}

func scanAlias(row rowScanner) (domain.Alias, error) {
	var alias domain.Alias

	err := row.Scan(
		&alias.ID,
		&alias.ImageID,
		&alias.Alias,
		&alias.VersionID,
		&alias.CreatedAt,
		&alias.UpdatedAt,
	)
	if err != nil {
		return domain.Alias{}, err
	}

	return alias, nil
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}
