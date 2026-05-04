package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/meigma/imgsrv/internal/catalog"
)

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// GetAlias looks up an image alias.
func (store *Store) GetAlias(ctx context.Context, params domain.GetAliasParams) (domain.Alias, error) {
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
			aliases.created_at,
			aliases.updated_at
		FROM aliases
		INNER JOIN images ON images.id = aliases.image_id
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

// ResolveManifest resolves the published manifest for an exact image version.
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

	manifest, err := resolveManifestHeader(ctx, db, params)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	manifest.Artifacts, err = resolveManifestArtifacts(ctx, db, manifest.Version.ID)
	if err != nil {
		return domain.Manifest{}, mapCatalogError(err)
	}

	return manifest, nil
}

func resolveManifestHeader(
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

func listArtifacts(ctx context.Context, db queryer, versionID uuid.UUID) ([]domain.Artifact, error) {
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
