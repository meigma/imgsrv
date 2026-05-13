---
title: Deploy imgsrv
description: Run imgsrv in production against PostgreSQL and S3-compatible object storage.
sidebar_position: 1
---

# Deploy imgsrv

`imgsrv` runs as a single Go binary or a distroless container image. Both
deployments depend on the same two external services:

- **PostgreSQL** for the control plane (auth, uploads, catalog, publish jobs,
  Simple Streams projection).
- **S3-compatible object storage** for the data plane (staging objects and CAS
  blobs). Garage is the expected backend, but any S3-compatible service
  exposing multipart upload works.

This page covers running the server in production. For the full configuration
surface, see [Configuration](../reference/configuration.md).

## Container image

The release pipeline publishes a distroless image to `ghcr.io/meigma/imgsrv`,
tagged with the release version. The image runs as non-root user `65532`,
exposes ports `8080` (API) and `9464` (metrics), and stops on `SIGTERM`.

```bash
docker run --rm \
  -p 8080:8080 \
  -p 127.0.0.1:9464:9464 \
  -e IMGSRV_POSTGRES_URL='postgres://imgsrv:secret@db.internal:5432/imgsrv?sslmode=require' \
  -e IMGSRV_S3_ENDPOINT='objects.internal:3900' \
  -e IMGSRV_S3_BUCKET='imgsrv' \
  -e IMGSRV_S3_ACCESS_KEY_ID='imgsrv' \
  -e IMGSRV_S3_SECRET_ACCESS_KEY='…' \
  -e IMGSRV_S3_USE_TLS=true \
  -e IMGSRV_CAS_PROMOTION_ENABLED=true \
  ghcr.io/meigma/imgsrv:<release-tag>
```

The image sets sensible defaults: `IMGSRV_LISTEN=:8080`,
`IMGSRV_LOG_FORMAT=json`, `IMGSRV_METRICS_LISTEN=:9464`. Override with
environment variables or flags.

## Binary

Build or download the binary and run it directly:

```bash
imgsrv \
  --postgres-url 'postgres://imgsrv:secret@db.internal:5432/imgsrv?sslmode=require' \
  --s3-endpoint 'objects.internal:3900' \
  --s3-bucket imgsrv \
  --s3-access-key-id imgsrv \
  --s3-secret-access-key "$SECRET" \
  --s3-use-tls \
  --cas-promotion-enabled \
  --log-format json \
  --listen :8080
```

Flag values take precedence over environment variables when both are set.

## PostgreSQL

`imgsrv` opens the database with the supplied URL and applies embedded
migrations at startup. There is no separate migration step.

The role `imgsrv` connects as needs full DDL on its schema during migrations
and read/write on tables thereafter. A typical bootstrap:

```sql
CREATE ROLE imgsrv LOGIN PASSWORD '…';
CREATE DATABASE imgsrv OWNER imgsrv;
```

For TLS, append `?sslmode=require` (or stricter) to the URL.

When `IMGSRV_POSTGRES_URL` is empty, `imgsrv` starts without database wiring.
Routes whose backing adapter requires the catalog return `503 Service
Unavailable`. This mode is useful only for liveness, readiness, and metrics
checks.

## S3-compatible object storage

`imgsrv` writes staged upload parts under `staging/uploads/<upload_id>` and
CAS objects under digest-derived keys. The configured bucket should be
empty at first launch and dedicated to `imgsrv`.

Garage with path-style addressing is the expected backend. Set
`IMGSRV_S3_PATH_STYLE=true` for Garage and any other path-style provider.
Virtual-hosted-style providers (AWS S3 itself, R2) leave it `false`.

`IMGSRV_S3_USE_TLS=true` enables HTTPS for the endpoint. Credentials and an
optional session token are supplied through the matching environment
variables or flags; see [Configuration](../reference/configuration.md#s3-compatible-object-storage).

The CAS promotion worker (`--cas-promotion-enabled`) is required for uploads
to progress from `completed` to `ready`. Disable it only in deployments where
a separate process runs the promotion workflow against the same database.

## TLS and reverse proxy

`imgsrv` does not terminate TLS itself. Put the API listener behind a reverse
proxy (NGINX, Caddy, Envoy, or a load balancer) that terminates TLS, forwards
the request unchanged, and preserves the original `Host` header.

The proxy must allow request bodies as large as the largest expected upload
part. Default limits in many proxies — 1 MiB, 10 MiB — will cause upload-part
requests to fail well before reaching the service. Tune the proxy to match
the part sizes the publishers actually send.

The metrics listener at port `9464` is intended for internal scraping only.
Bind it to a loopback or private address (`127.0.0.1:9464` or
`10.0.0.1:9464`) rather than exposing it through the reverse proxy.

## Exposed endpoints

| Listener | Endpoint | Purpose |
| --- | --- | --- |
| API (`--listen`) | `GET /healthz` | Liveness. Returns `204` unconditionally. |
| API (`--listen`) | `GET /readyz` | Readiness. Returns `204` when the readiness checker reports ready, `503` otherwise. |
| API (`--listen`) | `/v1/*`, `/streams/v1/*` | Public API and Simple Streams projection. |
| Metrics (`--metrics-listen`) | `/metrics` (path configurable) | Prometheus scrape. |

For wiring `/healthz` and `/readyz` into a load balancer, see
[Operate imgsrv](./operate.md#health-and-readiness).
