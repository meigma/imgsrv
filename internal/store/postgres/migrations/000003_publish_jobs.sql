-- +goose Up
ALTER TABLE image_versions
    DROP CONSTRAINT image_versions_state_check;

ALTER TABLE image_versions
    ADD CONSTRAINT image_versions_state_check
    CHECK (state IN ('draft', 'publishing', 'published'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION image_versions_guard_write() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state IS DISTINCT FROM 'draft'
            OR NEW.published_at IS NOT NULL
        THEN
            RAISE EXCEPTION 'image_versions must be inserted as drafts' USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.id IS DISTINCT FROM NEW.id
        OR OLD.image_id IS DISTINCT FROM NEW.image_id
        OR OLD.version IS DISTINCT FROM NEW.version
        OR OLD.created_at IS DISTINCT FROM NEW.created_at
    THEN
        RAISE EXCEPTION 'image_versions identity fields are immutable' USING ERRCODE = '23514';
    END IF;

    IF OLD.state = 'published' THEN
        IF OLD.state IS DISTINCT FROM NEW.state
            OR OLD.published_at IS DISTINCT FROM NEW.published_at
            OR OLD.updated_at IS DISTINCT FROM NEW.updated_at
        THEN
            RAISE EXCEPTION 'published image_versions are immutable' USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.state = NEW.state THEN
        RETURN NEW;
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'publishing' THEN
        IF NEW.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'publishing image_versions must not set published_at'
                USING ERRCODE = '23514';
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM release_artifacts
            WHERE version_id = NEW.id
        ) THEN
            RAISE EXCEPTION 'published image_versions require at least one release_artifact'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM release_artifacts AS artifact
            LEFT JOIN cas_blobs AS blob
                ON blob.digest = artifact.primary_blob_digest
                    AND blob.size_bytes = artifact.primary_blob_size_bytes
            WHERE artifact.version_id = NEW.id
                AND blob.digest IS NULL
        ) THEN
            RAISE EXCEPTION 'published image_versions require verified primary blobs'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM artifact_attachments AS attachment
            INNER JOIN release_artifacts AS artifact
                ON artifact.id = attachment.artifact_id
            LEFT JOIN cas_blobs AS blob
                ON blob.digest = attachment.blob_digest
                    AND blob.size_bytes = attachment.blob_size_bytes
            WHERE artifact.version_id = NEW.id
                AND blob.digest IS NULL
        ) THEN
            RAISE EXCEPTION 'published image_versions require verified attachment blobs'
                USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.state = 'publishing' AND NEW.state = 'published' THEN
        IF NEW.published_at IS NULL THEN
            RAISE EXCEPTION 'published image_versions require published_at'
                USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'invalid image_versions state transition' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd

CREATE TABLE publish_jobs (
    id UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES image_versions (id),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    failure_message TEXT CHECK (failure_message IS NULL OR length(btrim(failure_message)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (state NOT IN ('succeeded', 'failed') OR finished_at IS NOT NULL),
    CHECK (state <> 'failed' OR failure_message IS NOT NULL)
);

CREATE UNIQUE INDEX publish_jobs_version_unique_idx
    ON publish_jobs (version_id);

CREATE INDEX publish_jobs_state_idx
    ON publish_jobs (state, updated_at, id);

CREATE TABLE publish_job_steps (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES publish_jobs (id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name IN ('validate_catalog', 'incus_index', 'finalize_publish')),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'skipped')),
    blocking BOOLEAN NOT NULL DEFAULT true,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    run_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by TEXT CHECK (locked_by IS NULL OR length(btrim(locked_by)) > 0),
    locked_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    failure_message TEXT CHECK (failure_message IS NULL OR length(btrim(failure_message)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((locked_by IS NULL) = (locked_at IS NULL)),
    CHECK ((state = 'running') = (locked_by IS NOT NULL AND locked_at IS NOT NULL)),
    CHECK (state NOT IN ('succeeded', 'failed', 'skipped') OR finished_at IS NOT NULL),
    CHECK (state <> 'failed' OR failure_message IS NOT NULL)
);

CREATE UNIQUE INDEX publish_job_steps_name_unique_idx
    ON publish_job_steps (job_id, name);

CREATE UNIQUE INDEX publish_job_steps_sequence_unique_idx
    ON publish_job_steps (job_id, sequence);

CREATE INDEX publish_job_steps_claim_idx
    ON publish_job_steps (run_after, sequence, id)
    WHERE state = 'queued';

CREATE INDEX publish_job_steps_stale_running_idx
    ON publish_job_steps (locked_at, sequence, id)
    WHERE state = 'running';

CREATE UNIQUE INDEX release_artifacts_id_version_unique_idx
    ON release_artifacts (id, version_id);

CREATE UNIQUE INDEX artifact_attachments_id_artifact_unique_idx
    ON artifact_attachments (id, artifact_id);

CREATE TABLE incus_projection_items (
    artifact_id UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES image_versions (id),
    metadata_attachment_id UUID NOT NULL,
    metadata_path TEXT NOT NULL CHECK (length(btrim(metadata_path)) > 0),
    disk_path TEXT NOT NULL CHECK (length(btrim(disk_path)) > 0),
    metadata_sha256 TEXT NOT NULL CHECK (metadata_sha256 ~ '^[0-9a-f]{64}$'),
    metadata_size_bytes BIGINT NOT NULL CHECK (metadata_size_bytes >= 0),
    disk_sha256 TEXT NOT NULL CHECK (disk_sha256 ~ '^[0-9a-f]{64}$'),
    disk_size_bytes BIGINT NOT NULL CHECK (disk_size_bytes >= 0),
    combined_disk_kvm_img_sha256 TEXT NOT NULL CHECK (combined_disk_kvm_img_sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (artifact_id, version_id)
        REFERENCES release_artifacts (id, version_id)
        ON DELETE CASCADE,
    FOREIGN KEY (metadata_attachment_id, artifact_id)
        REFERENCES artifact_attachments (id, artifact_id)
        ON DELETE CASCADE
);

CREATE INDEX incus_projection_items_version_idx
    ON incus_projection_items (version_id);

-- +goose Down
DROP TABLE incus_projection_items;
DROP INDEX artifact_attachments_id_artifact_unique_idx;
DROP INDEX release_artifacts_id_version_unique_idx;
DROP TABLE publish_job_steps;
DROP TABLE publish_jobs;

ALTER TABLE image_versions
    DROP CONSTRAINT image_versions_state_check;

ALTER TABLE image_versions
    ADD CONSTRAINT image_versions_state_check
    CHECK (state IN ('draft', 'published'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION image_versions_guard_write() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state IS DISTINCT FROM 'draft'
            OR NEW.published_at IS NOT NULL
        THEN
            RAISE EXCEPTION 'image_versions must be inserted as drafts' USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.id IS DISTINCT FROM NEW.id
        OR OLD.image_id IS DISTINCT FROM NEW.image_id
        OR OLD.version IS DISTINCT FROM NEW.version
        OR OLD.created_at IS DISTINCT FROM NEW.created_at
    THEN
        RAISE EXCEPTION 'image_versions identity fields are immutable' USING ERRCODE = '23514';
    END IF;

    IF OLD.state = 'published' THEN
        IF OLD.state IS DISTINCT FROM NEW.state
            OR OLD.published_at IS DISTINCT FROM NEW.published_at
            OR OLD.updated_at IS DISTINCT FROM NEW.updated_at
        THEN
            RAISE EXCEPTION 'published image_versions are immutable' USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'draft' THEN
        RETURN NEW;
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'published' THEN
        IF NOT EXISTS (
            SELECT 1
            FROM release_artifacts
            WHERE version_id = NEW.id
        ) THEN
            RAISE EXCEPTION 'published image_versions require at least one release_artifact'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM release_artifacts AS artifact
            LEFT JOIN cas_blobs AS blob
                ON blob.digest = artifact.primary_blob_digest
                    AND blob.size_bytes = artifact.primary_blob_size_bytes
            WHERE artifact.version_id = NEW.id
                AND blob.digest IS NULL
        ) THEN
            RAISE EXCEPTION 'published image_versions require verified primary blobs'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM artifact_attachments AS attachment
            INNER JOIN release_artifacts AS artifact
                ON artifact.id = attachment.artifact_id
            LEFT JOIN cas_blobs AS blob
                ON blob.digest = attachment.blob_digest
                    AND blob.size_bytes = attachment.blob_size_bytes
            WHERE artifact.version_id = NEW.id
                AND blob.digest IS NULL
        ) THEN
            RAISE EXCEPTION 'published image_versions require verified attachment blobs'
                USING ERRCODE = '23514';
        END IF;

        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'invalid image_versions state transition' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
