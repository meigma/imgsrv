---
title: Architecture
description: Architecture of the imgsrv image artifact service.
---

# Architecture

`imgsrv` is a disk and VM image artifact service for operators. It stores bytes
in a verified content-addressed store, records image catalog state in
PostgreSQL, publishes immutable image versions, and serves artifacts through
stable HTTP routes.

The architecture is similar to a small OCI-style registry, but for whole disk
and VM artifacts instead of container layers. Raw bytes are uploaded and verified
independently from the image versions that give those bytes release meaning.

## Runtime Shape

The service runs as one Go binary with configuration exposed through matching
CLI flags and environment variables. The HTTP server uses standard-library
routing, `log/slog` for logs, OpenTelemetry metrics with a Prometheus endpoint,
and a hand-written OpenAPI contract.

PostgreSQL is the control plane. It stores auth state, upload state, trusted CAS
blob records, image catalog records, durable publish jobs, and Simple Streams
projection rows. Migrations are embedded in the binary and applied at startup
when PostgreSQL is configured.

S3-compatible object storage is the data plane. Uploads write staged objects,
CAS promotion verifies those staged bytes, and published downloads are served
through `imgsrv` from digest-addressed objects. Garage is the expected object
store, but the adapter is configured with generic S3-compatible settings.

## Content Model

### CAS Blobs

A CAS blob is a verified object addressed by `sha256:<hex>`. Blob records track
the digest, size, storage key, verification state, and timestamps.

Clients may claim an expected digest during upload, but the digest becomes
trusted only after CAS promotion verifies the stored bytes. Readers never consume
staging objects.

### Images And Versions

An image is an operator-defined namespace. A version belongs to an image and
starts as a mutable draft. Publishing freezes the version, validates its
referenced blobs, and marks it immutable.

Draft versions can reference CAS digests before the corresponding blobs are
trusted. Publishing fails until every referenced primary artifact and attachment
exists as a verified CAS blob.

### Release Artifacts

A release artifact is a manifest entry on an image version. It references one
primary CAS blob and records release-specific metadata such as operating system,
architecture, format, blob size, digest, and media type.

The blob remains reusable content. The release artifact is the version-scoped
context that explains how that blob should be consumed.

### Attachments

An attachment is a secondary CAS blob associated with one release artifact.
Attachments carry a name and media type. Typical attachments include checksums,
signatures, SBOMs, vendor metadata, or Incus metadata.

For the Incus Simple Streams projection, a published `qcow2` artifact with an
`incus.tar.xz` attachment becomes eligible for the Simple Streams image product
document.

### Aliases

Aliases are mutable pointers to immutable published versions. They are scoped to
an image name, point to one published version, and can move without changing the
target version. Version strings remain stable references; aliases provide names
such as `latest`.

## Upload And CAS Promotion

Uploads are content-centric. They populate CAS with bytes for an expected digest
and do not target a specific image, version, or artifact.

The upload flow follows S3 multipart semantics while keeping object storage
behind the service:

1. The client begins an upload with expected digest and size.
2. `imgsrv` returns an upload ID.
3. The client uploads numbered parts to `imgsrv`.
4. `imgsrv` streams each part into an internal multipart upload.
5. The client completes the upload with the accepted part list.
6. The internal multipart upload is completed into a staging object.
7. CAS promotion verifies the staged bytes and records the trusted CAS blob.

Upload state is durable and retry-friendly. Re-uploading the same part number
before completion replaces that part. Completing an upload that is already
completed, ingesting, or ready returns the current session state.

## Publishing

Publishing is the immutability boundary for image versions. The publish API
freezes a draft as `publishing` and enqueues ordered durable publish steps.

The publish workflow validates catalog preconditions, verifies that every
referenced CAS blob is trusted, writes Incus projection rows for eligible
artifacts, and finalizes the image version as `published`.

Publish jobs are stored in PostgreSQL. Workers claim steps with leases, record
attempts and failures, and preserve enough state for operators to inspect or
retry failed jobs. Retrying a failed job requeues from the first failed blocking
step.

## HTTP Surface

The API exposes these route groups:

- Operational: `GET /healthz`, `GET /readyz`
- Auth management: `/v1/auth/*`
- Uploads and trusted blobs: `/v1/uploads/*`, `/v1/blobs/{digest}`
- Image catalog and manifests: `/v1/images/*`
- Publish jobs: `/v1/publish-jobs/*`
- Incus Simple Streams: `/streams/v1/index.json`, `/streams/v1/images.json`

Bearer authentication protects writes and auth-management actions. Read routes
serve published catalog state, trusted blob data, upload status, and Simple
Streams documents when their backing adapters are configured.

The implemented endpoint contract is
[OpenAPI v1](/openapi/v1.yaml).

## Trust Boundaries

`imgsrv` enforces logical immutability through narrow write paths, PostgreSQL
constraints, transactional catalog updates, digest-addressed object keys, and
publish-time validation.

Object storage remains an internal implementation detail. Published downloads
are proxied through `imgsrv` so clients use stable service URLs instead of
direct S3-compatible URLs.

Operator-level database or object-store access is outside the service trust
boundary. Deployments should protect PostgreSQL and object storage as privileged
infrastructure.
