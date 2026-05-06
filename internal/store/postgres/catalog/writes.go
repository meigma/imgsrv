package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

// CreateImage creates an image namespace.
func (store *Store) CreateImage(ctx context.Context, params domain.CreateImageParams) (domain.Image, error) {
	if err := validateCreateImageParams(params); err != nil {
		return domain.Image{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Image{}, err
	}

	image, err := scanImage(db.QueryRow(
		ctx,
		`INSERT INTO images (id, name, display_name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, display_name, description, created_at, updated_at`,
		uuid.New(),
		params.Name,
		params.DisplayName,
		params.Description,
	))
	if err != nil {
		return domain.Image{}, mapCatalogError(err)
	}

	return image, nil
}

// CreateDraftVersion creates a mutable draft version.
func (store *Store) CreateDraftVersion(
	ctx context.Context,
	params domain.CreateDraftVersionParams,
) (domain.Version, error) {
	if err := validateCreateDraftVersionParams(params); err != nil {
		return domain.Version{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Version{}, err
	}

	version, err := scanVersion(db.QueryRow(
		ctx,
		`INSERT INTO image_versions (id, image_id, version, state)
		SELECT $1, images.id, $3, 'draft'
		FROM images
		WHERE images.name = $2
		RETURNING id, image_id, version, state, published_at, created_at, updated_at`,
		uuid.New(),
		params.ImageName,
		params.Version,
	))
	if err != nil {
		return domain.Version{}, mapCatalogError(err)
	}

	return version, nil
}

// AddArtifact adds a primary artifact declaration to a draft version.
func (store *Store) AddArtifact(ctx context.Context, params domain.AddArtifactParams) (domain.Artifact, error) {
	if err := validateAddArtifactParams(params); err != nil {
		return domain.Artifact{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Artifact{}, err
	}

	artifact, err := scanArtifact(db.QueryRow(
		ctx,
		`INSERT INTO release_artifacts (
			id,
			version_id,
			operating_system,
			architecture,
			format,
			primary_blob_digest,
			primary_blob_size_bytes,
			primary_media_type
		)
		SELECT $1, image_versions.id, $4, $5, $6, $7, $8, $9
		FROM image_versions
		INNER JOIN images ON images.id = image_versions.image_id
		WHERE images.name = $2
			AND image_versions.version = $3
		RETURNING id,
			version_id,
			operating_system,
			architecture,
			format,
			primary_blob_digest,
			primary_blob_size_bytes,
			primary_media_type,
			created_at,
			updated_at`,
		uuid.New(),
		params.ImageName,
		params.Version,
		params.OperatingSystem,
		params.Architecture,
		params.Format,
		params.PrimaryBlobDigest,
		params.PrimaryBlobSizeBytes,
		params.PrimaryMediaType,
	))
	if err != nil {
		return domain.Artifact{}, mapCatalogError(err)
	}

	return artifact, nil
}

// AddAttachment adds an attachment declaration to a draft artifact.
func (store *Store) AddAttachment(
	ctx context.Context,
	params domain.AddAttachmentParams,
) (domain.Attachment, error) {
	if err := validateAddAttachmentParams(params); err != nil {
		return domain.Attachment{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Attachment{}, err
	}

	attachment, err := scanAttachment(db.QueryRow(
		ctx,
		`INSERT INTO artifact_attachments (
			id,
			artifact_id,
			name,
			media_type,
			blob_digest,
			blob_size_bytes
		)
		SELECT $1, release_artifacts.id, $5, $6, $7, $8
		FROM release_artifacts
		INNER JOIN image_versions ON image_versions.id = release_artifacts.version_id
		INNER JOIN images ON images.id = image_versions.image_id
		WHERE images.name = $2
			AND image_versions.version = $3
			AND release_artifacts.id = $4
		RETURNING id,
			artifact_id,
			name,
			media_type,
			blob_digest,
			blob_size_bytes,
			created_at,
			updated_at`,
		uuid.New(),
		params.ImageName,
		params.Version,
		params.ArtifactID,
		params.Name,
		params.MediaType,
		params.BlobDigest,
		params.BlobSizeBytes,
	))
	if err != nil {
		return domain.Attachment{}, mapCatalogError(err)
	}

	return attachment, nil
}

// DeleteArtifact removes a primary artifact from a draft version.
func (store *Store) DeleteArtifact(ctx context.Context, params domain.DeleteArtifactParams) error {
	if err := validateDeleteArtifactParams(params); err != nil {
		return err
	}

	err := store.withTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(
			ctx,
			`DELETE FROM artifact_attachments
			USING release_artifacts, image_versions, images
			WHERE artifact_attachments.artifact_id = release_artifacts.id
				AND release_artifacts.version_id = image_versions.id
				AND image_versions.image_id = images.id
				AND images.name = $1
				AND image_versions.version = $2
				AND release_artifacts.id = $3`,
			params.ImageName,
			params.Version,
			params.ArtifactID,
		)
		if err != nil {
			return err
		}

		var id uuid.UUID
		return tx.QueryRow(
			ctx,
			`DELETE FROM release_artifacts
			USING image_versions, images
			WHERE release_artifacts.version_id = image_versions.id
				AND image_versions.image_id = images.id
				AND images.name = $1
				AND image_versions.version = $2
				AND release_artifacts.id = $3
			RETURNING release_artifacts.id`,
			params.ImageName,
			params.Version,
			params.ArtifactID,
		).Scan(&id)
	})
	if err != nil {
		return mapCatalogError(err)
	}

	return nil
}

// DeleteAttachment removes an attachment from a draft artifact.
func (store *Store) DeleteAttachment(ctx context.Context, params domain.DeleteAttachmentParams) error {
	if err := validateDeleteAttachmentParams(params); err != nil {
		return err
	}

	db, err := store.catalogDB()
	if err != nil {
		return err
	}

	var id uuid.UUID
	err = db.QueryRow(
		ctx,
		`DELETE FROM artifact_attachments
		USING release_artifacts, image_versions, images
		WHERE artifact_attachments.artifact_id = release_artifacts.id
			AND release_artifacts.version_id = image_versions.id
			AND image_versions.image_id = images.id
			AND images.name = $1
			AND image_versions.version = $2
			AND release_artifacts.id = $3
			AND artifact_attachments.id = $4
		RETURNING artifact_attachments.id`,
		params.ImageName,
		params.Version,
		params.ArtifactID,
		params.AttachmentID,
	).Scan(&id)
	if err != nil {
		return mapCatalogError(err)
	}

	return nil
}

// PublishVersion publishes a draft version.
func (store *Store) PublishVersion(
	ctx context.Context,
	params domain.PublishVersionParams,
) (domain.Version, error) {
	if err := validatePublishVersionParams(params); err != nil {
		return domain.Version{}, err
	}

	var version domain.Version
	err := store.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		version, scanErr = scanVersion(tx.QueryRow(
			ctx,
			`UPDATE image_versions
			SET state = 'published',
				published_at = now(),
				updated_at = now()
			FROM images
			WHERE images.id = image_versions.image_id
				AND images.name = $1
				AND image_versions.version = $2
			RETURNING image_versions.id,
				image_versions.image_id,
				image_versions.version,
				image_versions.state,
				image_versions.published_at,
				image_versions.created_at,
				image_versions.updated_at`,
			params.ImageName,
			params.Version,
		))

		return scanErr
	})
	if err != nil {
		return domain.Version{}, mapCatalogError(err)
	}

	return version, nil
}

// PutAlias creates or moves an alias to a published version.
func (store *Store) PutAlias(ctx context.Context, params domain.PutAliasParams) (domain.Alias, error) {
	if err := validatePutAliasParams(params); err != nil {
		return domain.Alias{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Alias{}, err
	}

	alias, err := scanAlias(db.QueryRow(
		ctx,
		`WITH target_version AS (
			SELECT images.id AS image_id,
				image_versions.id AS version_id,
				image_versions.version
			FROM images
			INNER JOIN image_versions ON image_versions.image_id = images.id
			WHERE images.name = $1
				AND image_versions.version = $3
		)
		INSERT INTO aliases (id, image_id, alias, version_id)
		SELECT $4, target_version.image_id, $2, target_version.version_id
		FROM target_version
		ON CONFLICT (image_id, alias)
		DO UPDATE SET version_id = excluded.version_id,
			updated_at = now()
		RETURNING aliases.id,
			aliases.image_id,
			aliases.alias,
			aliases.version_id,
			(SELECT target_version.version FROM target_version),
			aliases.created_at,
			aliases.updated_at`,
		params.ImageName,
		params.Alias,
		params.Version,
		uuid.New(),
	))
	if err != nil {
		return domain.Alias{}, mapCatalogError(err)
	}

	return alias, nil
}

// DeleteAlias removes an image alias.
func (store *Store) DeleteAlias(ctx context.Context, params domain.DeleteAliasParams) error {
	if err := validateDeleteAliasParams(params); err != nil {
		return err
	}

	db, err := store.catalogDB()
	if err != nil {
		return err
	}

	var id uuid.UUID
	err = db.QueryRow(
		ctx,
		`DELETE FROM aliases
		USING images
		WHERE aliases.image_id = images.id
			AND images.name = $1
			AND aliases.alias = $2
		RETURNING aliases.id`,
		params.ImageName,
		params.Alias,
	).Scan(&id)
	if err != nil {
		return mapCatalogError(err)
	}

	return nil
}
