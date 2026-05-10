-- +goose Up
CREATE DOMAIN imgsrv_sha256_digest AS TEXT
    CHECK (VALUE ~ '^sha256:[0-9a-f]{64}$');

CREATE TABLE cas_blobs (
    digest imgsrv_sha256_digest PRIMARY KEY,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    storage_key TEXT NOT NULL,
    media_type TEXT CHECK (media_type IS NULL OR length(btrim(media_type)) > 0),
    verified_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        storage_key = 'cas/sha256/'
            || substr(digest, 8, 2)
            || '/'
            || substr(digest, 10, 2)
            || '/'
            || substr(digest, 8)
    )
);

CREATE UNIQUE INDEX cas_blobs_storage_key_unique_idx
    ON cas_blobs (storage_key);

CREATE TABLE upload_sessions (
    id UUID PRIMARY KEY,
    expected_digest imgsrv_sha256_digest NOT NULL,
    expected_size_bytes BIGINT NOT NULL CHECK (expected_size_bytes >= 0),
    state TEXT NOT NULL CHECK (
        state IN ('created', 'uploading', 'completed', 'ingesting', 'ready', 'failed', 'aborted')
    ),
    storage_upload_id TEXT NOT NULL CHECK (length(btrim(storage_upload_id)) > 0),
    staging_key TEXT NOT NULL,
    media_type_hint TEXT CHECK (media_type_hint IS NULL OR length(btrim(media_type_hint)) > 0),
    filename_hint TEXT CHECK (filename_hint IS NULL OR length(btrim(filename_hint)) > 0),
    completed_at TIMESTAMPTZ,
    ingest_started_at TIMESTAMPTZ,
    ready_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    aborted_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    failure_message TEXT CHECK (failure_message IS NULL OR length(btrim(failure_message)) > 0),
    ready_blob_digest imgsrv_sha256_digest REFERENCES cas_blobs (digest),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (staging_key = 'staging/uploads/' || id::TEXT),
    CHECK (expires_at > created_at),
    CHECK (ready_blob_digest IS NULL OR ready_blob_digest = expected_digest),
    CHECK ((state = 'ready') = (ready_blob_digest IS NOT NULL AND ready_at IS NOT NULL)),
    CHECK ((state = 'failed') = (failed_at IS NOT NULL AND failure_message IS NOT NULL)),
    CHECK ((state = 'aborted') = (aborted_at IS NOT NULL)),
    CHECK (state NOT IN ('completed', 'ingesting', 'ready') OR completed_at IS NOT NULL),
    CHECK (state <> 'ingesting' OR ingest_started_at IS NOT NULL)
);

CREATE UNIQUE INDEX upload_sessions_storage_upload_unique_idx
    ON upload_sessions (storage_upload_id);

CREATE UNIQUE INDEX upload_sessions_staging_key_unique_idx
    ON upload_sessions (staging_key);

CREATE INDEX upload_sessions_expected_digest_idx
    ON upload_sessions (expected_digest);

CREATE INDEX upload_sessions_state_idx
    ON upload_sessions (state, updated_at, id);

CREATE INDEX upload_sessions_completed_idx
    ON upload_sessions (completed_at, id)
    WHERE state = 'completed';

CREATE TABLE upload_parts (
    upload_id UUID NOT NULL REFERENCES upload_sessions (id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    etag TEXT NOT NULL CHECK (length(btrim(etag)) > 0),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, part_number)
);

CREATE TABLE cas_ingest_jobs (
    id UUID PRIMARY KEY,
    upload_id UUID NOT NULL REFERENCES upload_sessions (id),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    run_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by TEXT CHECK (locked_by IS NULL OR length(btrim(locked_by)) > 0),
    locked_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    failure_message TEXT CHECK (failure_message IS NULL OR length(btrim(failure_message)) > 0),
    blob_digest imgsrv_sha256_digest REFERENCES cas_blobs (digest),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((locked_by IS NULL) = (locked_at IS NULL)),
    CHECK ((state = 'running') = (locked_by IS NOT NULL AND locked_at IS NOT NULL)),
    CHECK ((state = 'succeeded') = (blob_digest IS NOT NULL AND finished_at IS NOT NULL)),
    CHECK (state <> 'failed' OR finished_at IS NOT NULL)
);

CREATE UNIQUE INDEX cas_ingest_jobs_upload_unique_idx
    ON cas_ingest_jobs (upload_id);

CREATE INDEX cas_ingest_jobs_claim_idx
    ON cas_ingest_jobs (run_after, id)
    WHERE state = 'queued';

CREATE INDEX cas_ingest_jobs_stale_running_idx
    ON cas_ingest_jobs (locked_at)
    WHERE state = 'running';

CREATE TABLE images (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    display_name TEXT CHECK (display_name IS NULL OR length(btrim(display_name)) > 0),
    description TEXT CHECK (description IS NULL OR length(btrim(description)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX images_name_unique_idx
    ON images (name);

CREATE TABLE image_versions (
    id UUID PRIMARY KEY,
    image_id UUID NOT NULL REFERENCES images (id),
    version TEXT NOT NULL CHECK (version ~ '^[A-Za-z0-9][A-Za-z0-9._+:-]{0,127}$'),
    state TEXT NOT NULL CHECK (state IN ('draft', 'published')),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state = 'published') = (published_at IS NOT NULL))
);

CREATE UNIQUE INDEX image_versions_identity_unique_idx
    ON image_versions (image_id, version);

CREATE INDEX image_versions_published_idx
    ON image_versions (image_id, published_at DESC, id)
    WHERE state = 'published';

CREATE TABLE release_artifacts (
    id UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES image_versions (id),
    operating_system TEXT NOT NULL CHECK (
        operating_system ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$'
    ),
    architecture TEXT NOT NULL CHECK (architecture ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$'),
    format TEXT NOT NULL CHECK (format IN ('raw', 'qcow2')),
    primary_blob_digest imgsrv_sha256_digest NOT NULL,
    primary_blob_size_bytes BIGINT NOT NULL CHECK (primary_blob_size_bytes >= 0),
    primary_media_type TEXT NOT NULL CHECK (length(btrim(primary_media_type)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX release_artifacts_identity_unique_idx
    ON release_artifacts (version_id, operating_system, architecture, format);

CREATE INDEX release_artifacts_primary_blob_idx
    ON release_artifacts (primary_blob_digest);

CREATE TABLE artifact_attachments (
    id UUID PRIMARY KEY,
    artifact_id UUID NOT NULL REFERENCES release_artifacts (id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$'),
    media_type TEXT NOT NULL CHECK (length(btrim(media_type)) > 0),
    blob_digest imgsrv_sha256_digest NOT NULL,
    blob_size_bytes BIGINT NOT NULL CHECK (blob_size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX artifact_attachments_name_unique_idx
    ON artifact_attachments (artifact_id, name);

CREATE INDEX artifact_attachments_blob_idx
    ON artifact_attachments (blob_digest);

CREATE TABLE aliases (
    id UUID PRIMARY KEY,
    image_id UUID NOT NULL REFERENCES images (id),
    alias TEXT NOT NULL CHECK (alias ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    version_id UUID NOT NULL REFERENCES image_versions (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX aliases_identity_unique_idx
    ON aliases (image_id, alias);

CREATE INDEX aliases_version_idx
    ON aliases (version_id);

-- +goose StatementBegin
CREATE FUNCTION imgsrv_reject_change() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '%', TG_ARGV[0] USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cas_blobs_no_update
BEFORE UPDATE ON cas_blobs
FOR EACH ROW
EXECUTE FUNCTION imgsrv_reject_change('cas_blobs are immutable');

CREATE TRIGGER cas_blobs_no_delete
BEFORE DELETE ON cas_blobs
FOR EACH ROW
EXECUTE FUNCTION imgsrv_reject_change('cas_blobs are immutable');

-- +goose StatementBegin
CREATE FUNCTION imgsrv_require_version_draft(target_version_id UUID) RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM image_versions
        WHERE id = target_version_id
            AND state = 'draft'
    ) THEN
        RAISE EXCEPTION 'version manifest can only change while draft' USING ERRCODE = '23514';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION image_versions_guard_write() RETURNS trigger
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

CREATE TRIGGER image_versions_guard_write
BEFORE INSERT OR UPDATE ON image_versions
FOR EACH ROW
EXECUTE FUNCTION image_versions_guard_write();

-- +goose StatementBegin
CREATE FUNCTION release_artifacts_require_draft() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_version_id UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        target_version_id := OLD.version_id;
    ELSE
        target_version_id := NEW.version_id;
    END IF;

    PERFORM imgsrv_require_version_draft(target_version_id);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER release_artifacts_require_draft
BEFORE INSERT OR UPDATE OR DELETE ON release_artifacts
FOR EACH ROW
EXECUTE FUNCTION release_artifacts_require_draft();

-- +goose StatementBegin
CREATE FUNCTION artifact_attachments_require_draft() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_artifact_id UUID;
    target_version_id UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        target_artifact_id := OLD.artifact_id;
    ELSE
        target_artifact_id := NEW.artifact_id;
    END IF;

    SELECT version_id INTO target_version_id
    FROM release_artifacts
    WHERE id = target_artifact_id;

    IF target_version_id IS NULL THEN
        RAISE EXCEPTION 'artifact attachment requires an existing release_artifact'
            USING ERRCODE = '23503';
    END IF;

    PERFORM imgsrv_require_version_draft(target_version_id);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER artifact_attachments_require_draft
BEFORE INSERT OR UPDATE OR DELETE ON artifact_attachments
FOR EACH ROW
EXECUTE FUNCTION artifact_attachments_require_draft();

-- +goose StatementBegin
CREATE FUNCTION aliases_guard_write() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF OLD.id IS DISTINCT FROM NEW.id
            OR OLD.image_id IS DISTINCT FROM NEW.image_id
            OR OLD.alias IS DISTINCT FROM NEW.alias
            OR OLD.created_at IS DISTINCT FROM NEW.created_at
        THEN
            RAISE EXCEPTION 'alias identity fields are immutable' USING ERRCODE = '23514';
        END IF;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM image_versions
        WHERE id = NEW.version_id
            AND image_id = NEW.image_id
            AND state = 'published'
    ) THEN
        RAISE EXCEPTION 'aliases must target a published version for the same image'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER aliases_guard_write
BEFORE INSERT OR UPDATE ON aliases
FOR EACH ROW
EXECUTE FUNCTION aliases_guard_write();

-- +goose Down
DROP TABLE aliases;
DROP TABLE artifact_attachments;
DROP TABLE release_artifacts;
DROP TABLE image_versions;
DROP TABLE images;
DROP TABLE cas_ingest_jobs;
DROP TABLE upload_parts;
DROP TABLE upload_sessions;
DROP TABLE cas_blobs;

DROP FUNCTION aliases_guard_write();
DROP FUNCTION artifact_attachments_require_draft();
DROP FUNCTION release_artifacts_require_draft();
DROP FUNCTION image_versions_guard_write();
DROP FUNCTION imgsrv_require_version_draft(UUID);
DROP FUNCTION imgsrv_reject_change();

DROP DOMAIN imgsrv_sha256_digest;
