-- +goose Up
ALTER TABLE release_artifacts
    DROP CONSTRAINT release_artifacts_format_check;

ALTER TABLE release_artifacts
    ADD CONSTRAINT release_artifacts_format_check
    CHECK (format IN ('raw', 'raw.gz', 'qcow2'));

-- +goose Down
ALTER TABLE release_artifacts
    DROP CONSTRAINT release_artifacts_format_check;

ALTER TABLE release_artifacts
    ADD CONSTRAINT release_artifacts_format_check
    CHECK (format IN ('raw', 'qcow2'));
