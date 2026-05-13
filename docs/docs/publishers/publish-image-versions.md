---
title: Publish image versions
description: The canonical immutable release flow — upload, draft, publish, alias.
sidebar_position: 1
---

# Publish image versions

`imgsrv` enforces an explicit separation between uploading bytes and giving
those bytes release meaning. The publishing flow is the prescribed sequence
that respects that boundary. Following it produces an immutable published
version. Skipping or reordering steps either fails outright or leaves a draft
that cannot be published.

For the model that shapes this flow, see
[Publishing model](../concepts/publishing-model.md). For the wire contract,
see [`/openapi/v1.yaml`](pathname:///openapi/v1.yaml).

Every example assumes a `content-writer` bearer token in `$TOKEN` and the
service at `$IMGSRV` (e.g. `https://imgsrv.example.com`).

## The flow at a glance

1. Upload the primary artifact bytes into CAS.
2. Upload any attachment bytes into CAS.
3. Wait for both uploads to reach `ready`.
4. Create the image (if it does not yet exist) and a draft version.
5. Attach the primary artifact to the draft version, by digest.
6. Attach any attachments to the artifact, by digest.
7. Publish the draft version.
8. Poll the publish job to a terminal state.
9. Move an alias (e.g. `latest`) to the newly published version.

Each step is idempotent and retry-friendly. The upload steps tolerate
re-sending parts; the publish step tolerates re-issuing the publish for an
already-publishing or already-published version.

## 1–2. Upload bytes into CAS

Uploads are content-centric: they populate CAS for a known digest. They do
not target an image or version. The same upload session can be referenced by
any number of future draft versions.

```bash
# 1. Begin the upload. Provide the expected sha256 and final size.
curl -sf -X POST "$IMGSRV/v1/uploads" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"expected_digest": "sha256:'"$DIGEST"'", "expected_size_bytes": '"$SIZE"'}'
# Response contains the upload session id; capture it as $UPLOAD_ID.

# 2. Upload each numbered part. Parts are 1-based; the response carries the
#    server-assigned ETag and accepted byte count.
curl -sf -X PUT \
  "$IMGSRV/v1/uploads/$UPLOAD_ID/parts/1" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@./image-part-001"

# 3. Complete the upload with the accepted part list (number + etag + size).
curl -sf -X POST \
  "$IMGSRV/v1/uploads/$UPLOAD_ID/complete" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"parts": [{"number": 1, "etag": "'"$PART_ETAG"'", "size_bytes": '"$PART_SIZE"'}]}'
```

If the expected digest already exists as a trusted CAS blob, the initial
`POST /v1/uploads` may short-circuit and return a session already in `ready`.

Repeat for each attachment (signatures, SBOMs, Incus metadata).

## 3. Wait for ready

A completed upload still needs to be promoted to a trusted CAS blob by the
CAS-promotion worker. Poll the session until it reports `ready`:

```bash
curl -sf "$IMGSRV/v1/uploads/$UPLOAD_ID" \
  -H "Authorization: Bearer $TOKEN" | jq .state
```

The session moves through `completed` → `ingesting` → `ready`. A `failed`
session needs operator attention before its bytes can be referenced. The full
state machine is at [States and roles](../reference/states-and-roles.md#upload-sessions).

The publish step refuses to proceed unless every referenced digest is
`ready`. Always confirm before drafting.

## 4. Create the image and draft version

The image is the operator-defined namespace; the version is the unit of
release meaning.

```bash
# Create the image (skip if it already exists).
curl -sf -X POST "$IMGSRV/v1/images" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "debian-bookworm"}'

# Create a draft version under the image.
curl -sf -X POST "$IMGSRV/v1/images/debian-bookworm/versions" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"version": "12.5.0"}'
```

The version arrives in state `draft`. Its manifest is empty.

## 5. Attach primary artifacts

A release artifact references one primary CAS blob by digest and records the
metadata that gives it release meaning.

```bash
curl -sf -X POST \
  "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0/artifacts" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "operating_system": "debian",
    "architecture": "amd64",
    "format": "qcow2",
    "primary_blob_digest": "sha256:'"$PRIMARY_DIGEST"'",
    "primary_blob_size_bytes": '"$PRIMARY_SIZE"',
    "primary_media_type": "application/x-qemu-disk"
  }'
```

A version may carry several artifacts — one per OS/architecture/format
combination. Each is added independently.

## 6. Attach attachments

Attachments are secondary blobs scoped to one artifact: signatures, SBOMs,
checksum bundles, or vendor metadata.

```bash
curl -sf -X POST \
  "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0/artifacts/$ARTIFACT_ID/attachments" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "incus.tar.xz",
    "blob_digest": "sha256:'"$INCUS_METADATA_DIGEST"'",
    "blob_size_bytes": '"$INCUS_METADATA_SIZE"',
    "media_type": "application/x-incus-metadata"
  }'
```

### Simple Streams eligibility

A qcow2 artifact with an attached `incus.tar.xz` metadata blob is eligible
for inclusion in the Incus Simple Streams projection. No additional flag is
required: at publish time, eligible artifacts are projected into
`/streams/v1/images.json`. See
[Consume images](./consume-images.md#incus-simple-streams) for the consumer
side.

## 7. Publish

Publishing freezes the manifest and enqueues the durable publish job.

```bash
curl -sf -X POST \
  "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0/publish" \
  -H "Authorization: Bearer $TOKEN"
```

The response contains the publish job ID. The version transitions to
`publishing` immediately.

If the version is already in `publishing` or `published`, the request returns
the current state without enqueueing a second job. This makes the publish
call safe to retry from a CI workflow that lost its connection.

## 8. Poll the publish job

The publish job runs `validate_catalog`, `incus_index`, and
`finalize_publish` in order. Poll until the job is `succeeded` or `failed`:

```bash
curl -sf "$IMGSRV/v1/publish-jobs/$JOB_ID" \
  -H "Authorization: Bearer $TOKEN" | jq '{state, failed_step, reason}'
```

On `succeeded`, the version is `published` and immutable.

On `failed`, the response identifies the step that failed and a
human-readable reason. After resolving the underlying issue (commonly: a
referenced upload was not yet `ready`, or an operator-visible infrastructure
problem), retry:

```bash
curl -sf -X POST \
  "$IMGSRV/v1/publish-jobs/$JOB_ID/retry" \
  -H "Authorization: Bearer $TOKEN"
```

Retry requeues from the first failed blocking step. Earlier successful steps
are not re-run. The full lifecycle is at
[States and roles](../reference/states-and-roles.md#publish-jobs).

## 9. Move an alias

Aliases are mutable pointers from a name to one published version. They are
scoped to the image. Move an alias only after the publish job is
`succeeded`.

```bash
curl -sf -X PUT \
  "$IMGSRV/v1/images/debian-bookworm/aliases/latest" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"version": "12.5.0"}'
```

A common convention:

- `latest` tracks the most recent published version.
- `stable` tracks the most recent fully validated release.
- Channel aliases (`testing`, `unstable`) track per-channel heads.

Aliases never point at drafts and never travel backward in time without
explicit operator action. A consumer following `latest` always lands on a
published, immutable version.

## Recovery patterns

| Situation | Action |
| --- | --- |
| Upload session reached `failed` | Begin a new upload session for the same digest. The original session does not consume CAS state. |
| Publish job failed at `validate_catalog` | Confirm every referenced upload is `ready`; promote any stuck `completed`/`ingesting` sessions; retry the job. |
| Publish job failed at `incus_index` or `finalize_publish` | Investigate the operator-visible reason; retry once the underlying issue clears. |
| Version stuck in `publishing` indefinitely | The publish job is still in a non-terminal state. There is no operator path to abandon publish in v0.1. |
| Alias pointing at the wrong version | Re-issue `PUT /v1/images/{name}/aliases/{alias}` with the correct version. Aliases overwrite. |

For deeper triage from the operator side, see
[Operate imgsrv](../operators/operate.md#triage-a-failed-publish-job).
