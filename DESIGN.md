# imgsrv v0 Design

`imgsrv` is a distributed disk image store for operational teams working in
non-cloud and on-prem infrastructure. Operators publish VM and disk image
artifacts to `imgsrv`; other tools later query and download those artifacts
through a web API.

This document defines enough architecture to build a true v0 prototype. It is
not a complete reference manual.

## Scope

v0 proves the native `imgsrv` API plus a first Incus-compatible read projection:

- upload content into a verified content-addressed store
- create draft image versions
- attach primary artifacts and attachments to draft versions by digest
- publish immutable versions
- manage aliases that point to versions
- query published images and versions
- download published artifacts through `imgsrv`
- serve an unsigned Simple Streams view for published qcow2 artifacts with
  `incus.tar.xz` metadata attachments

The Simple Streams view is intentionally a live projection, not a persisted
materialization table or background job. It exists to prove Incus compatibility
before adding a broader materialization framework.

## Implementation Baseline

The service is written in Go. The HTTP API uses standard-library HTTP components.
The binary has a single root entrypoint with configuration exposed as flag and
environment-variable pairs.

PostgreSQL is the shared control plane. Migrations are hand-written and managed
by Goose. Persistence code uses a bespoke typed interface backed by raw SQL; no
ORM is used. PostgreSQL access uses `github.com/jackc/pgx/v5`.

Garage provides the distributed object storage data plane through its
S3-compatible API. `imgsrv` talks to Garage with `github.com/minio/minio-go/v7`.
Garage should be treated as an internal implementation detail: clients talk to
`imgsrv`, not directly to Garage, in the default v0 path.

Logging uses `log/slog` with text and JSON output modes. Metrics use
OpenTelemetry with a Prometheus endpoint. The OpenAPI v3 specification is
hand-written.

## Core Model

The architecture is similar to a small OCI-style registry, but for disk and VM
images rather than containers. It uses the same broad split between verified
content-addressed blobs and manifests that give those blobs meaning.

`imgsrv` does not model layers. A blob is a whole uploaded file, such as a raw
image, qcow2 image, metadata file, signature, SBOM, or other artifact file.

### Content Blobs

A content blob is a verified object in CAS.

Blob identities use `sha256:<hex>`.

Blob records track at least:

- digest
- size
- verified state
- storage key
- creation time

Only the CAS ingest path can create trusted blob records. An operator-provided
digest is a claim until `imgsrv` has verified bytes matching that digest.

### Images and Versions

An image has an operator-defined unique name.

A version belongs to an image. Versions begin as drafts and become immutable when
published. Once a version is published, its manifest content cannot change.

Draft versions may cite blobs that do not exist in CAS yet. Publishing fails
until every cited blob exists and has been verified by CAS ingest.

### Release Artifacts

A release artifact is a manifest entry on a version. It references one primary
CAS blob by digest and gives that blob release-specific meaning.

Release artifact metadata includes at least:

- operating system
- architecture
- format, such as `raw` or `qcow2`
- primary blob digest
- primary blob size
- primary media type

The blob remains content-level data. The release artifact is the context that
says what the blob means for a specific image version.

### Artifact Attachments

Attachments are a v0 feature.

An artifact attachment is an additional CAS blob explicitly associated with a
release artifact. Attachments have a role or name and a MIME/media type. Examples
include signatures, SBOMs, checksum bundles, vendor metadata, or future
materialization-specific metadata.

This keeps third-party metadata out of the core artifact schema. A qcow2 artifact
without Incus-specific metadata is still a valid native `imgsrv` artifact, but it
is not automatically eligible for an Incus/simplestreams materialization later.

### Aliases

Aliases are mutable pointers to immutable versions.

- aliases are scoped to an image name
- each alias points to exactly one version
- alias values have only sane clean-text validation
- aliases can move between versions
- aliases point only to published versions
- versions themselves never move

For example, `myimage:latest` can point to `myimage` version `v1.0.0`. Later,
`latest` may move to `v1.0.1`, but `v1.0.0` remains immutable.

## Upload and CAS Ingest

Uploads are content-centric, not release-centric. An upload does not target a
specific image, version, or artifact. It exists to populate CAS with bytes for an
expected digest.

The upload API should map closely to S3 multipart semantics while keeping Garage
hidden behind `imgsrv`.

The basic flow is:

1. The client initiates an upload with expected digest and size.
2. `imgsrv` returns an upload ID.
3. The client uploads numbered parts to `imgsrv`.
4. `imgsrv` streams each part into an internal S3 multipart upload in Garage.
5. The client completes the upload with the expected part list.
6. `imgsrv` completes the internal multipart upload into a staging object.
7. A background CAS ingest job verifies the staged object and records the trusted
   CAS blob.

Upload requests may carry optional content hints, but release artifacts and
attachments are the places where media type gives a blob meaning.

If the expected digest already exists in the trusted CAS catalog, upload
initiation can return an already-present result so the client can skip the
transfer.

### Upload State

The prototype should implement a small durable upload state machine:

- `created`: upload session exists
- `uploading`: at least one part has been accepted
- `completed`: multipart upload has been completed into staging
- `ingesting`: a worker is verifying and promoting the staged object
- `ready`: the expected digest exists as a trusted CAS blob
- `failed`: ingest failed
- `aborted`: upload was explicitly aborted or expired before ingest

Part upload should be retry-friendly. Re-uploading the same part number before
completion replaces the previously recorded part for that upload session, matching
S3 multipart behavior. Completing an upload should be safe to retry: if the
session is already completed, ingesting, or ready, the API returns the current
state rather than creating a second staged object.

## Storage Layout

Readers never consume staging objects.

Uploads write to staging keys derived from the upload ID. CAS ingest verifies the
staged object and then ensures the final digest-addressed object exists.

The exact sharding scheme can be simple, such as:

- staging object: `staging/uploads/{upload_id}`
- CAS object: `cas/sha256/{first_two_hex}/{next_two_hex}/{hex}`

Releases do not own S3 folders. Published versions reference CAS digests. If two
versions cite the same digest, they share the same CAS blob.

Garage does not provide the logical immutability guarantee. PostgreSQL and
`imgsrv` enforce the catalog rules, while CAS object naming prevents normal
application paths from overwriting published content with different bytes.

## Draft and Publish Flow

Release construction is separate from upload.

The native API should allow operators to:

1. create a draft version for an image
2. declare primary artifacts by digest
3. declare artifact attachments by digest
4. add or remove artifact declarations while the version is still draft
5. publish the draft once every cited digest exists in the trusted CAS catalog

Publishing is the immutability boundary. Before publish, a draft is operator
controlled. After publish, the version manifest cannot change.

Publishing validates at least:

- every cited primary blob exists in CAS
- every cited attachment blob exists in CAS
- each cited blob has been verified against its `sha256:<hex>` digest

More validation can be added during the prototype, but v0 should stay centered on
CAS correctness.

## Reads and Downloads

Published query and download surfaces are anonymous in v0. Write, draft, upload,
publish, and alias mutation APIs require API-token authorization.

Downloads are proxied through `imgsrv` by default. This keeps Garage hidden,
keeps clients on stable `imgsrv` URLs, and avoids surprising compatibility
clients with redirects, expiring URLs, or S3-specific behavior.

Pre-signed direct upload or download URLs may be added later as an optimization
for native clients or high-throughput deployments. They are not part of the v0
baseline.

## Jobs

v0 needs one durable background job class: CAS ingest.

CAS ingest workers claim completed uploads from PostgreSQL, verify staged bytes,
ensure the digest-addressed CAS object exists, record the trusted blob, and clean
up staging state when safe.

The current Incus Simple Streams route is a live projection over published
release manifests, CAS blobs, and artifact attachments. Future jobs can generate
artifact attachments when operators ask for them, and future materializers may
maintain projection tables for cheap serving. Those tables are caches or views;
the source of truth remains the published release manifest, CAS blob catalog,
and artifact attachments.

## API Sketch

Exact paths can change during implementation. The prototype should preserve these
operations.

Content upload:

- `POST /v1/uploads`
- `PUT /v1/uploads/{upload_id}/parts/{part_number}`
- `POST /v1/uploads/{upload_id}/complete`
- `POST /v1/uploads/{upload_id}/abort`
- `GET /v1/uploads/{upload_id}`
- `GET /v1/blobs/{digest}`

Image and version catalog:

- `POST /v1/images`
- `GET /v1/images`
- `GET /v1/images/{name}`
- `POST /v1/images/{name}/versions`
- `GET /v1/images/{name}/versions`
- `GET /v1/images/{name}/versions/{version}`
- `POST /v1/images/{name}/versions/{version}/publish`

Draft manifest editing:

- `POST /v1/images/{name}/versions/{version}/artifacts`
- `DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}`
- `POST /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments`
- `DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}`

Reads and downloads:

- `GET /v1/images/{name}/versions/{version}/artifacts`
- `GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}`
- `GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/download`
- `GET /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}/download`

Aliases:

- `PUT /v1/images/{name}/aliases/{alias}`
- `GET /v1/images/{name}/aliases`
- `GET /v1/images/{name}/aliases/{alias}`
- `DELETE /v1/images/{name}/aliases/{alias}`

Read APIs should support resolving an alias to its target version for friendly
references such as `myimage:latest`. Exact route shape can be settled while
writing the OpenAPI spec.

## Immutability and Trust

v0 provides best-effort immutability.

`imgsrv` and PostgreSQL enforce immutable published versions with transactions,
constraints, narrow write paths, and database guards. CAS objects are addressed by
digest and should never be mutated through normal service paths.

This does not protect against a privileged database or storage operator rewriting
state out of band. Later versions may add a Rekor-like transparency layer,
signed publication records, or another external append-only audit mechanism for
environments where operator trust is not guaranteed.

## Deferred Work

The following are intentionally outside v0:

- persisted/static simplestreams materialization jobs
- other protocol-specific materializations
- `tus` upload support
- direct pre-signed upload/download optimization
- multiple authentication methods
- private download policy
- transparency log integration
- advanced deduplication beyond digest-addressed CAS
