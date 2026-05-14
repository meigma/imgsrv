-- +goose Up
ALTER TABLE release_artifacts
    ADD COLUMN variant TEXT NOT NULL DEFAULT 'default';

ALTER TABLE release_artifacts
    ADD CONSTRAINT release_artifacts_variant_check
    CHECK (variant ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$');

DROP INDEX release_artifacts_identity_unique_idx;

CREATE UNIQUE INDEX release_artifacts_identity_unique_idx
    ON release_artifacts (version_id, variant, operating_system, architecture, format);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM release_artifacts
        GROUP BY version_id, operating_system, architecture, format
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back release artifact variants while duplicate non-variant artifact identities exist';
    END IF;
END;
$$;
-- +goose StatementEnd

DROP INDEX release_artifacts_identity_unique_idx;

CREATE UNIQUE INDEX release_artifacts_identity_unique_idx
    ON release_artifacts (version_id, operating_system, architecture, format);

ALTER TABLE release_artifacts
    DROP CONSTRAINT release_artifacts_variant_check;

ALTER TABLE release_artifacts
    DROP COLUMN variant;
