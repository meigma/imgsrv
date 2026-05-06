package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

// queryer is the subset of the pgx connection surface used by catalog read helpers.
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// getImageID resolves imageName to an image ID and maps a missing image to
// the catalog not-found sentinel.
func getImageID(ctx context.Context, db queryer, imageName string) (uuid.UUID, error) {
	var imageID uuid.UUID
	err := db.QueryRow(ctx, `SELECT id FROM images WHERE name = $1`, imageName).Scan(&imageID)
	if err != nil {
		return uuid.Nil, mapCatalogError(err)
	}

	return imageID, nil
}

// getPublicImageID resolves imageName to an image ID only when the image has
// at least one published version.
func getPublicImageID(ctx context.Context, db queryer, imageName string) (uuid.UUID, error) {
	var imageID uuid.UUID
	err := db.QueryRow(
		ctx,
		`SELECT id
		FROM images
		WHERE name = $1
			AND EXISTS (
				SELECT 1
				FROM image_versions
				WHERE image_versions.image_id = images.id
					AND image_versions.state = 'published'
			)`,
		imageName,
	).Scan(&imageID)
	if err != nil {
		return uuid.Nil, mapCatalogError(err)
	}

	return imageID, nil
}

// collectRows scans a pgx row set into a typed domain slice and always closes
// the row cursor.
func collectRows[T any](rows pgx.Rows, scan func(rowScanner) (T, error)) ([]T, error) {
	defer rows.Close()

	var items []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// ListImages returns image namespaces with published versions ordered by image name.
func (store *Store) ListImages(
	ctx context.Context,
	params domain.ListImagesParams,
) ([]domain.Image, error) {
	if err := validateListImagesParams(params); err != nil {
		return nil, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		ctx,
		`SELECT id, name, display_name, description, created_at, updated_at
		FROM images
		WHERE EXISTS (
			SELECT 1
			FROM image_versions
			WHERE image_versions.image_id = images.id
				AND image_versions.state = 'published'
		)
		ORDER BY name, id`,
	)
	if err != nil {
		return nil, err
	}

	return collectRows(rows, scanImage)
}

// GetImage returns one image namespace by name when it has a published version.
func (store *Store) GetImage(
	ctx context.Context,
	params domain.GetImageParams,
) (domain.Image, error) {
	if err := validateGetImageParams(params); err != nil {
		return domain.Image{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Image{}, err
	}

	image, err := scanImage(db.QueryRow(
		ctx,
		`SELECT id, name, display_name, description, created_at, updated_at
		FROM images
		WHERE name = $1
			AND EXISTS (
				SELECT 1
				FROM image_versions
				WHERE image_versions.image_id = images.id
					AND image_versions.state = 'published'
			)`,
		params.Name,
	))
	if err != nil {
		return domain.Image{}, mapCatalogError(err)
	}

	return image, nil
}

// GetAlias looks up an image alias.
func (store *Store) GetAlias(
	ctx context.Context,
	params domain.GetAliasParams,
) (domain.Alias, error) {
	if err := validateGetAliasParams(params); err != nil {
		return domain.Alias{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Alias{}, err
	}

	alias, err := scanAlias(db.QueryRow(
		ctx,
		`SELECT aliases.id,
			aliases.image_id,
			aliases.alias,
			aliases.version_id,
			image_versions.version,
			aliases.created_at,
			aliases.updated_at
		FROM aliases
		INNER JOIN images ON images.id = aliases.image_id
		INNER JOIN image_versions ON image_versions.id = aliases.version_id
		WHERE images.name = $1
			AND aliases.alias = $2`,
		params.ImageName,
		params.Alias,
	))
	if err != nil {
		return domain.Alias{}, mapCatalogError(err)
	}

	return alias, nil
}

// ListAliases returns aliases for one image ordered by alias name.
func (store *Store) ListAliases(
	ctx context.Context,
	params domain.ListAliasesParams,
) ([]domain.Alias, error) {
	if err := validateListAliasesParams(params); err != nil {
		return nil, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return nil, err
	}

	imageID, err := getImageID(ctx, db, params.ImageName)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		ctx,
		`SELECT aliases.id,
			aliases.image_id,
			aliases.alias,
			aliases.version_id,
			image_versions.version,
			aliases.created_at,
			aliases.updated_at
		FROM aliases
		INNER JOIN image_versions ON image_versions.id = aliases.version_id
		WHERE aliases.image_id = $1
		ORDER BY aliases.alias, aliases.id`,
		imageID,
	)
	if err != nil {
		return nil, err
	}

	return collectRows(rows, scanAlias)
}

// ListVersions returns published versions for one image ordered by creation time descending.
func (store *Store) ListVersions(
	ctx context.Context,
	params domain.ListVersionsParams,
) ([]domain.Version, error) {
	if err := validateListVersionsParams(params); err != nil {
		return nil, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return nil, err
	}

	imageID, err := getPublicImageID(ctx, db, params.ImageName)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		ctx,
		`SELECT id, image_id, version, state, published_at, created_at, updated_at
		FROM image_versions
		WHERE image_id = $1
			AND state = 'published'
		ORDER BY created_at DESC, version, id`,
		imageID,
	)
	if err != nil {
		return nil, err
	}

	return collectRows(rows, scanVersion)
}

// ListPublishedArtifacts returns primary artifacts for an exact published image version.
func (store *Store) ListPublishedArtifacts(
	ctx context.Context,
	params domain.ListPublishedArtifactsParams,
) ([]domain.Artifact, error) {
	if err := validateListPublishedArtifactsParams(params); err != nil {
		return nil, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return nil, err
	}

	manifest, err := resolveExactPublishedManifestHeader(
		ctx,
		db,
		domain.ResolveManifestParams(params),
	)
	if err != nil {
		return nil, mapCatalogError(err)
	}

	artifacts, err := listArtifacts(ctx, db, manifest.Version.ID)
	if err != nil {
		return nil, mapCatalogError(err)
	}

	return artifacts, nil
}

// GetPublishedArtifact returns one primary artifact for an exact published image version.
func (store *Store) GetPublishedArtifact(
	ctx context.Context,
	params domain.GetPublishedArtifactParams,
) (domain.Artifact, error) {
	if err := validateGetPublishedArtifactParams(params); err != nil {
		return domain.Artifact{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Artifact{}, err
	}

	artifact, err := scanArtifact(db.QueryRow(
		ctx,
		`SELECT release_artifacts.id,
			release_artifacts.version_id,
			release_artifacts.operating_system,
			release_artifacts.architecture,
			release_artifacts.format,
			release_artifacts.primary_blob_digest,
			release_artifacts.primary_blob_size_bytes,
			release_artifacts.primary_media_type,
			release_artifacts.created_at,
			release_artifacts.updated_at
		FROM images
		INNER JOIN image_versions ON image_versions.image_id = images.id
		INNER JOIN release_artifacts ON release_artifacts.version_id = image_versions.id
		WHERE images.name = $1
			AND image_versions.version = $2
			AND image_versions.state = 'published'
			AND release_artifacts.id = $3`,
		params.ImageName,
		params.Version,
		params.ArtifactID,
	))
	if err != nil {
		return domain.Artifact{}, mapCatalogError(err)
	}

	return artifact, nil
}

// GetPublishedAttachment returns one attachment for an exact published image version artifact.
func (store *Store) GetPublishedAttachment(
	ctx context.Context,
	params domain.GetPublishedAttachmentParams,
) (domain.Attachment, error) {
	if err := validateGetPublishedAttachmentParams(params); err != nil {
		return domain.Attachment{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Attachment{}, err
	}

	attachment, err := scanAttachment(db.QueryRow(
		ctx,
		`SELECT artifact_attachments.id,
			artifact_attachments.artifact_id,
			artifact_attachments.name,
			artifact_attachments.media_type,
			artifact_attachments.blob_digest,
			artifact_attachments.blob_size_bytes,
			artifact_attachments.created_at,
			artifact_attachments.updated_at
		FROM images
		INNER JOIN image_versions ON image_versions.image_id = images.id
		INNER JOIN release_artifacts ON release_artifacts.version_id = image_versions.id
		INNER JOIN artifact_attachments ON artifact_attachments.artifact_id = release_artifacts.id
		WHERE images.name = $1
			AND image_versions.version = $2
			AND image_versions.state = 'published'
			AND release_artifacts.id = $3
			AND artifact_attachments.id = $4`,
		params.ImageName,
		params.Version,
		params.ArtifactID,
		params.AttachmentID,
	))
	if err != nil {
		return domain.Attachment{}, mapCatalogError(err)
	}

	return attachment, nil
}

// GetVersionManifest resolves the manifest for an exact draft or published image version.
func (store *Store) GetVersionManifest(
	ctx context.Context,
	params domain.GetVersionManifestParams,
) (domain.Manifest, error) {
	if err := validateGetVersionManifestParams(params); err != nil {
		return domain.Manifest{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Manifest{}, err
	}

	manifest, err := resolveVersionManifestHeader(ctx, db, params)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	manifest.Artifacts, err = resolveManifestArtifacts(ctx, db, manifest.Version.ID)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	return manifest, nil
}

// ResolveManifest resolves the published manifest for an exact image version or alias.
func (store *Store) ResolveManifest(
	ctx context.Context,
	params domain.ResolveManifestParams,
) (domain.Manifest, error) {
	if err := validateResolveManifestParams(params); err != nil {
		return domain.Manifest{}, err
	}

	db, err := store.catalogDB()
	if err != nil {
		return domain.Manifest{}, err
	}

	manifest, err := resolvePublishedManifestHeader(ctx, db, params)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	manifest.Artifacts, err = resolveManifestArtifacts(ctx, db, manifest.Version.ID)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	return manifest, nil
}

// resolveVersionManifestHeader loads the image and exact version that form the
// manifest header for the requested image and version.
func resolveVersionManifestHeader(
	ctx context.Context,
	db queryer,
	params domain.GetVersionManifestParams,
) (domain.Manifest, error) {
	row := db.QueryRow(
		ctx,
		`SELECT images.id,
			images.name,
			images.display_name,
			images.description,
			images.created_at,
			images.updated_at,
			image_versions.id,
			image_versions.image_id,
			image_versions.version,
			image_versions.state,
			image_versions.published_at,
			image_versions.created_at,
			image_versions.updated_at
		FROM images
		INNER JOIN image_versions ON image_versions.image_id = images.id
		WHERE images.name = $1
			AND image_versions.version = $2`,
		params.ImageName,
		params.Version,
	)

	image, version, err := scanImageAndVersion(row)
	if err != nil {
		return domain.Manifest{}, err
	}

	return domain.Manifest{Image: image, Version: version}, nil
}

// resolvePublishedManifestHeader loads the image and published version that form the
// manifest header for the requested image and version.
func resolvePublishedManifestHeader(
	ctx context.Context,
	db queryer,
	params domain.ResolveManifestParams,
) (domain.Manifest, error) {
	manifest, err := resolveExactPublishedManifestHeader(ctx, db, params)
	if err == nil {
		return manifest, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Manifest{}, err
	}

	return resolveAliasPublishedManifestHeader(ctx, db, params)
}

// resolveExactPublishedManifestHeader loads the exact published version
// manifest header for the requested image and version.
func resolveExactPublishedManifestHeader(
	ctx context.Context,
	db queryer,
	params domain.ResolveManifestParams,
) (domain.Manifest, error) {
	row := db.QueryRow(
		ctx,
		`SELECT images.id,
			images.name,
			images.display_name,
			images.description,
			images.created_at,
			images.updated_at,
			image_versions.id,
			image_versions.image_id,
			image_versions.version,
			image_versions.state,
			image_versions.published_at,
			image_versions.created_at,
			image_versions.updated_at
		FROM images
		INNER JOIN image_versions ON image_versions.image_id = images.id
		WHERE images.name = $1
			AND image_versions.version = $2
			AND image_versions.state = 'published'`,
		params.ImageName,
		params.Version,
	)

	image, version, err := scanImageAndVersion(row)
	if err != nil {
		return domain.Manifest{}, err
	}

	return domain.Manifest{Image: image, Version: version}, nil
}

// resolveAliasPublishedManifestHeader loads the published version manifest
// header targeted by an alias on the requested image.
func resolveAliasPublishedManifestHeader(
	ctx context.Context,
	db queryer,
	params domain.ResolveManifestParams,
) (domain.Manifest, error) {
	row := db.QueryRow(
		ctx,
		`SELECT images.id,
			images.name,
			images.display_name,
			images.description,
			images.created_at,
			images.updated_at,
			image_versions.id,
			image_versions.image_id,
			image_versions.version,
			image_versions.state,
			image_versions.published_at,
			image_versions.created_at,
			image_versions.updated_at
		FROM images
		INNER JOIN aliases ON aliases.image_id = images.id
		INNER JOIN image_versions ON image_versions.id = aliases.version_id
		WHERE images.name = $1
			AND aliases.alias = $2
			AND image_versions.image_id = images.id
			AND image_versions.state = 'published'`,
		params.ImageName,
		params.Version,
	)

	image, version, err := scanImageAndVersion(row)
	if err != nil {
		return domain.Manifest{}, err
	}

	return domain.Manifest{Image: image, Version: version}, nil
}

// resolveManifestArtifacts loads the artifacts for versionID and joins each
// with its attachments in stable catalog order.
func resolveManifestArtifacts(
	ctx context.Context,
	db queryer,
	versionID uuid.UUID,
) ([]domain.ManifestArtifact, error) {
	artifacts, err := listArtifacts(ctx, db, versionID)
	if err != nil {
		return nil, err
	}
	if len(artifacts) == 0 {
		return nil, nil
	}

	artifactIDs := make([]uuid.UUID, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifactIDs = append(artifactIDs, artifact.ID)
	}

	attachmentsByArtifact, err := listAttachmentsByArtifact(ctx, db, artifactIDs)
	if err != nil {
		return nil, err
	}

	manifestArtifacts := make([]domain.ManifestArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		manifestArtifacts = append(manifestArtifacts, domain.ManifestArtifact{
			Artifact:    artifact,
			Attachments: attachmentsByArtifact[artifact.ID],
		})
	}

	return manifestArtifacts, nil
}

// listArtifacts returns the release artifacts for versionID ordered by
// operating system, architecture, format, and id.
func listArtifacts(
	ctx context.Context,
	db queryer,
	versionID uuid.UUID,
) ([]domain.Artifact, error) {
	rows, err := db.Query(
		ctx,
		`SELECT id,
			version_id,
			operating_system,
			architecture,
			format,
			primary_blob_digest,
			primary_blob_size_bytes,
			primary_media_type,
			created_at,
			updated_at
		FROM release_artifacts
		WHERE version_id = $1
		ORDER BY operating_system, architecture, format, id`,
		versionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []domain.Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return artifacts, nil
}

// listAttachmentsByArtifact returns the attachments for the given artifactIDs
// grouped by parent artifact in stable catalog order.
func listAttachmentsByArtifact(
	ctx context.Context,
	db queryer,
	artifactIDs []uuid.UUID,
) (map[uuid.UUID][]domain.Attachment, error) {
	rows, err := db.Query(
		ctx,
		`SELECT id,
			artifact_id,
			name,
			media_type,
			blob_digest,
			blob_size_bytes,
			created_at,
			updated_at
		FROM artifact_attachments
		WHERE artifact_id = ANY($1::uuid[])
		ORDER BY artifact_id, name, id`,
		artifactIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attachments := make(map[uuid.UUID][]domain.Attachment)
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments[attachment.ArtifactID] = append(attachments[attachment.ArtifactID], attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return attachments, nil
}
