---
title: Server Guide
description: Runtime configuration and operational usage for the imgsrv server.
---

# Server Guide

`imgsrv` runs as a single Go binary. It exposes a public API listener for
operator and client traffic, and an optional Prometheus metrics listener for
monitoring.

## Runtime Dependencies

PostgreSQL is the control plane for auth, upload state, catalog state, CAS blob
records, publish jobs, and Incus projection rows. When `IMGSRV_POSTGRES_URL` is
set, the server opens PostgreSQL and applies embedded migrations before serving
traffic.

S3-compatible object storage is the data plane for uploaded and published bytes.
Garage is the expected storage backend, but the server configuration uses generic
S3-compatible endpoint, bucket, and credential settings.

The server can start without PostgreSQL or object storage for liveness,
readiness, and metrics checks. API routes whose backing adapters are not
configured return an unavailable response.

Configure PostgreSQL and S3-compatible object storage together for the full API.
The publish worker needs both catalog state and CAS blob reads. Enable the CAS
promotion worker when uploads should progress automatically from completed
staging objects to trusted CAS blobs.

## Configuration

Runtime settings are available as CLI flags and environment variables.

| Purpose | Flag | Environment |
| --- | --- | --- |
| API listen address | `--listen` | `IMGSRV_LISTEN` |
| Node name for worker IDs | `--node-name` | `IMGSRV_NODE_NAME` |
| Log format, `text` or `json` | `--log-format` | `IMGSRV_LOG_FORMAT` |
| Log verbosity | `--verbosity` | `IMGSRV_VERBOSITY` |
| Metrics listen address | `--metrics-listen` | `IMGSRV_METRICS_LISTEN` |
| Metrics path | `--metrics-path` | `IMGSRV_METRICS_PATH` |
| PostgreSQL URL | `--postgres-url` | `IMGSRV_POSTGRES_URL` |
| S3 endpoint | `--s3-endpoint` | `IMGSRV_S3_ENDPOINT` |
| S3 bucket | `--s3-bucket` | `IMGSRV_S3_BUCKET` |
| S3 access key ID | `--s3-access-key-id` | `IMGSRV_S3_ACCESS_KEY_ID` |
| S3 secret access key | `--s3-secret-access-key` | `IMGSRV_S3_SECRET_ACCESS_KEY` |
| S3 session token | `--s3-session-token` | `IMGSRV_S3_SESSION_TOKEN` |
| S3 region | `--s3-region` | `IMGSRV_S3_REGION` |
| S3 TLS mode | `--s3-use-tls` | `IMGSRV_S3_USE_TLS` |
| S3 path-style addressing | `--s3-path-style` | `IMGSRV_S3_PATH_STYLE` |
| Upload session lifetime | `--upload-ttl` | `IMGSRV_UPLOAD_TTL` |
| CAS promotion worker | `--cas-promotion-enabled` | `IMGSRV_CAS_PROMOTION_ENABLED` |
| CAS promotion poll interval | `--cas-promotion-poll-interval` | `IMGSRV_CAS_PROMOTION_POLL_INTERVAL` |
| CAS promotion retry backoff | `--cas-promotion-error-backoff` | `IMGSRV_CAS_PROMOTION_ERROR_BACKOFF` |
| CAS promotion max backoff | `--cas-promotion-error-backoff-max` | `IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX` |
| CAS promotion circuit limit | `--cas-promotion-circuit-breaker-failures` | `IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES` |
| CAS promotion circuit cooldown | `--cas-promotion-circuit-breaker-cooldown` | `IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN` |
| Graceful shutdown timeout | `--shutdown-timeout` | `IMGSRV_SHUTDOWN_TIMEOUT` |

Metrics default to `127.0.0.1:9464/metrics`. Set `IMGSRV_METRICS_LISTEN` or
`--metrics-listen` to an empty value to disable the metrics listener.

## Local Startup

Start the operational endpoints:

```sh
go run ./cmd/imgsrv --listen :8080
```

Start the API with PostgreSQL and S3-compatible object storage:

```sh
IMGSRV_POSTGRES_URL='postgres://imgsrv:imgsrv@localhost:5432/imgsrv?sslmode=disable' \
IMGSRV_S3_ENDPOINT='127.0.0.1:3900' \
IMGSRV_S3_BUCKET='imgsrv' \
IMGSRV_S3_ACCESS_KEY_ID='imgsrv' \
IMGSRV_S3_SECRET_ACCESS_KEY='imgsrv-secret' \
IMGSRV_S3_PATH_STYLE=true \
IMGSRV_CAS_PROMOTION_ENABLED=true \
go run ./cmd/imgsrv --listen :8080
```

Use `GET /healthz` for liveness and `GET /readyz` for readiness. Both return
`204 No Content` when healthy.

## Auth Onboarding

When a PostgreSQL-backed deployment has no `auth-manager` principal, startup
prints one bootstrap API token to stdout. The token is valid for initial auth
setup and is printed once.

Use the bootstrap token as a bearer token against `/v1/auth/*` to:

1. List built-in roles with `GET /v1/auth/roles`.
2. Create a service principal with `POST /v1/auth/principals`.
3. Assign `content-writer` or `auth-manager` roles to the principal.
4. Issue an API token with
   `POST /v1/auth/principals/{principal_id}/api-tokens`.
5. Store the returned plaintext token in the publishing system.

`auth-manager` grants access to auth-management endpoints. `content-writer`
grants upload, draft editing, publishing, publish-job retry, and alias mutation.

OIDC publishers are managed through `/v1/auth/oidc-provisioning-rules`.
Provisioning rules verify issuer and audience, forward selected JWT claims to a
CEL condition, and assign `content-writer` to identities that match the rule.

## Publishing Workflow

The publishing workflow separates raw bytes from catalog meaning:

1. Begin an upload with the expected `sha256` digest and final size.
2. Upload one or more numbered parts.
3. Complete the upload with the accepted part list.
4. Let the CAS promotion worker verify the staged object and record the trusted
   CAS blob.
5. Create an image namespace and draft version.
6. Add primary artifacts and attachments that reference trusted CAS digests.
7. Publish the draft version.
8. Poll the returned publish job until it reaches a terminal state.
9. Move aliases such as `latest` to the published version.

Publishing freezes the version, validates referenced blobs, writes Incus
projection rows for eligible artifacts, and marks the version as published. A
failed publish job can be retried with
`POST /v1/publish-jobs/{job_id}/retry` after the underlying problem is fixed.

## Read Surfaces

Published catalog and artifact reads are served by the native `/v1/images/*`
routes. Artifact and attachment downloads are proxied through `imgsrv`, keeping
object storage private and clients on stable service URLs.

The Incus-compatible Simple Streams projection is served at:

- `GET /streams/v1/index.json`
- `GET /streams/v1/images.json`

Eligible Simple Streams entries are produced from published `qcow2` artifacts
that include an `incus.tar.xz` attachment.

## API Contract

The implemented HTTP contract is available as
[OpenAPI v1](/openapi/v1.yaml).
