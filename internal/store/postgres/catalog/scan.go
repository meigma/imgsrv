package catalog

import (
	"database/sql"
	"time"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

// rowScanner is the minimal pgx row interface used by the catalog scan helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanImage decodes a single images row into the domain Image type, lifting
// nullable display name and description columns into optional pointer fields.
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

// scanVersion decodes a single image_versions row into the domain Version
// type, lifting the nullable published_at column into an optional pointer.
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

// scanImageAndVersion decodes a joined images and image_versions row into the
// domain Image and Version pair used to construct manifest headers.
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

// scanArtifact decodes a single release_artifacts row into the domain
// Artifact type.
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

// scanAttachment decodes a single artifact_attachments row into the domain
// Attachment type.
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

// scanAlias decodes a single aliases row into the domain Alias type.
func scanAlias(row rowScanner) (domain.Alias, error) {
	var alias domain.Alias

	err := row.Scan(
		&alias.ID,
		&alias.ImageID,
		&alias.Alias,
		&alias.VersionID,
		&alias.Version,
		&alias.CreatedAt,
		&alias.UpdatedAt,
	)
	if err != nil {
		return domain.Alias{}, err
	}

	return alias, nil
}

// optionalString returns a pointer to value's string when valid, or nil when
// the column was SQL NULL.
func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

// optionalTime returns a pointer to value's time when valid, or nil when the
// column was SQL NULL.
func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}

	return &value.Time
}
