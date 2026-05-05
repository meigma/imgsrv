package uploads

// sessionColumns enumerates the upload_sessions columns selected when
// scanning a domain.Session row, in the order scanSession expects.
const sessionColumns = `id,
	expected_digest,
	expected_size_bytes,
	state,
	storage_upload_id,
	staging_key,
	media_type_hint,
	filename_hint,
	completed_at,
	ingest_started_at,
	ready_at,
	failed_at,
	aborted_at,
	expires_at,
	failure_message,
	ready_blob_digest,
	created_at,
	updated_at`

// partColumns enumerates the upload_parts columns selected when scanning a
// domain.Part row, in the order scanPart expects.
const partColumns = `upload_id,
	part_number,
	etag,
	size_bytes,
	uploaded_at,
	updated_at`

// ingestJobColumns enumerates the cas_ingest_jobs columns selected when
// scanning a domain.IngestJob row, in the order scanIngestJob expects.
const ingestJobColumns = `id,
	upload_id,
	state,
	attempt_count,
	run_after,
	locked_by,
	locked_at,
	started_at,
	finished_at,
	failure_message,
	blob_digest,
	created_at,
	updated_at`
