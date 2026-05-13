---
title: Configuration
description: Authoritative reference for imgsrv flags and environment variables.
sidebar_position: 1
---

# Configuration

Every runtime setting is available as a CLI flag and an environment variable.
Flags take precedence when both are set. Defaults apply when neither is set.

Durations use Go's [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration)
syntax (`500ms`, `5s`, `1m`, `1h`).

## Server

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--listen` | `IMGSRV_LISTEN` | `:8080` | HTTP listen address for the public API. |
| `--node-name` | `IMGSRV_NODE_NAME` | _empty_ | Prefix used in background worker IDs. |
| `--shutdown-timeout` | `IMGSRV_SHUTDOWN_TIMEOUT` | `10s` | Graceful HTTP shutdown deadline. |

## Logging

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--log-format` | `IMGSRV_LOG_FORMAT` | `text` | Log encoding. One of `text`, `json`. |
| `--verbosity` | `IMGSRV_VERBOSITY` | `info` | Minimum log level. One of `debug`, `info`, `warn`, `error`. |

## Metrics

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--metrics-listen` | `IMGSRV_METRICS_LISTEN` | `127.0.0.1:9464` | Metrics HTTP listen address. Empty disables the metrics listener. |
| `--metrics-path` | `IMGSRV_METRICS_PATH` | `/metrics` | HTTP path that serves Prometheus metrics. |

## PostgreSQL

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--postgres-url` | `IMGSRV_POSTGRES_URL` | _empty_ | PostgreSQL connection URL. Empty skips database startup; routes whose backing adapter is unavailable return `503`. |

## S3-compatible object storage

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--s3-endpoint` | `IMGSRV_S3_ENDPOINT` | _empty_ | S3-compatible endpoint host and port without a URL scheme. |
| `--s3-bucket` | `IMGSRV_S3_BUCKET` | _empty_ | Bucket used for staging and CAS objects. |
| `--s3-access-key-id` | `IMGSRV_S3_ACCESS_KEY_ID` | _empty_ | S3 access key ID. |
| `--s3-secret-access-key` | `IMGSRV_S3_SECRET_ACCESS_KEY` | _empty_ | S3 secret access key. |
| `--s3-session-token` | `IMGSRV_S3_SESSION_TOKEN` | _empty_ | Optional temporary credential session token. |
| `--s3-region` | `IMGSRV_S3_REGION` | _empty_ | Optional region. |
| `--s3-use-tls` | `IMGSRV_S3_USE_TLS` | `false` | Use HTTPS for the S3-compatible endpoint. |
| `--s3-path-style` | `IMGSRV_S3_PATH_STYLE` | `false` | Use path-style bucket addressing instead of virtual-hosted-style. |

## Uploads

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--upload-ttl` | `IMGSRV_UPLOAD_TTL` | `24h` | Lifetime of a new upload session before it expires if not completed. |

## CAS promotion worker

The CAS promotion worker verifies completed upload sessions and records the
resulting CAS blobs. It is disabled by default.

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `--cas-promotion-enabled` | `IMGSRV_CAS_PROMOTION_ENABLED` | `false` | Run the in-process CAS promotion worker. |
| `--cas-promotion-poll-interval` | `IMGSRV_CAS_PROMOTION_POLL_INTERVAL` | `5s` | Idle poll interval. |
| `--cas-promotion-error-backoff` | `IMGSRV_CAS_PROMOTION_ERROR_BACKOFF` | `5s` | Initial delay after a failed promotion. |
| `--cas-promotion-error-backoff-max` | `IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX` | `1m` | Maximum delay between retries. |
| `--cas-promotion-circuit-breaker-failures` | `IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES` | `10` | Consecutive failures that open the circuit breaker. |
| `--cas-promotion-circuit-breaker-cooldown` | `IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN` | `1m` | Cooldown applied while the circuit breaker is open. |

## Build metadata

`--version` prints release version, commit, and build date, then exits. It has
no environment-variable equivalent.
