---
title: Metrics
description: Prometheus metrics exposed by the imgsrv server.
sidebar_position: 2
---

# Metrics

`imgsrv` exposes Prometheus-formatted metrics from a separate HTTP listener.
The listener address and path are controlled by [`--metrics-listen`](./configuration.md#metrics)
and `--metrics-path`. The default endpoint is `http://127.0.0.1:9464/metrics`.

The Prometheus scrape uses OpenMetrics formatting; both Prometheus and any
OpenMetrics-compatible scraper consume it directly.

## Labels

Application metrics intentionally use bounded labels only. Expect labels such as
`state`, `step`, `job`, `operation`, `outcome`, and `direction`. Metrics must not
include upload IDs, object keys, image names, version strings, digests, issuer
URLs, principals, or raw error text.

The OpenTelemetry Prometheus exporter also adds scope labels such as
`otel_scope_name`. Treat those as exporter metadata rather than imgsrv labels.

## HTTP series

The API listener is wrapped with OpenTelemetry HTTP instrumentation.

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `http_server_request_duration_seconds` | Histogram | `http_request_method`, `http_response_status_code`, `http_route`, `network_protocol_name`, `server_address`, `server_port`, `url_scheme` | Per-request server-side latency in seconds. Routes are reported using the matched `http.ServeMux` pattern, so values look like `GET /v1/images/{name}`. |
| `http_server_active_requests` | UpDownCounter | `http_request_method`, `url_scheme`, `server_address`, `server_port` | Number of in-flight requests on the public API listener. |

Exact label sets follow the OpenTelemetry
[HTTP semantic conventions](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/);
any addition or revision the upstream conventions make appears here without an
imgsrv code change.

## Application series

imgsrv emits application metrics only when the metrics listener is enabled.
Counters reset when the process restarts. Durable state gauges are read from
Postgres during scrapes, so they reflect current database state rather than
process memory.

### Postgres

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `imgsrv_postgres_pool_connections` | Gauge | `state` | Current pgx pool connections. `state` is `acquired`, `idle`, `constructing`, `total`, or `max`. |
| `imgsrv_postgres_pool_acquires_total` | Counter | none | Successful pool acquisitions. |
| `imgsrv_postgres_pool_empty_acquires_total` | Counter | none | Acquisitions that had to wait because the pool was empty. |
| `imgsrv_postgres_pool_canceled_acquires_total` | Counter | none | Pool acquisition attempts canceled by context. |
| `imgsrv_postgres_pool_acquire_duration_seconds_total` | Counter | none | Cumulative time spent acquiring pool connections. |

### Object Store

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `imgsrv_objectstore_operations_total` | Counter | `operation`, `outcome` | Object-store operation attempts. |
| `imgsrv_objectstore_operation_duration_seconds` | Histogram | `operation`, `outcome` | Object-store operation latency. |
| `imgsrv_objectstore_bytes_total` | Counter | `operation`, `direction` | Known bytes read or written through object-store operations. |

`outcome` is one of `success`, `not_found`, `conflict`, `already_exists`,
`invalid`, `canceled`, or `error`.

### Upload And CAS

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `imgsrv_upload_sessions` | Gauge | `state` | Upload sessions by durable state. |
| `imgsrv_cas_ingest_jobs` | Gauge | `state` | CAS ingest jobs by durable state. |
| `imgsrv_cas_ingest_oldest_queued_age_seconds` | Gauge | none | Age of the oldest due queued CAS ingest job. Absent when none exist. |
| `imgsrv_cas_ingest_oldest_running_age_seconds` | Gauge | none | Age of the oldest running CAS ingest job. Absent when none exist. |
| `imgsrv_cas_blobs` | Gauge | none | Verified CAS blob count. |
| `imgsrv_cas_blob_bytes` | Gauge | none | Total verified CAS blob bytes. |

CAS ingest currently exposes stuck running work but does not reclaim stale
running jobs. Alert on `imgsrv_cas_ingest_oldest_running_age_seconds` if it
exceeds the expected promotion runtime.

### Publish

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `imgsrv_publish_jobs` | Gauge | `state` | Publish jobs by durable state. |
| `imgsrv_publish_steps` | Gauge | `step`, `state` | Publish steps by step name and durable state. |
| `imgsrv_publish_step_oldest_queued_age_seconds` | Gauge | `step` | Age of the oldest due queued publish step by step. Absent when none exist. |
| `imgsrv_publish_step_oldest_running_age_seconds` | Gauge | `step` | Age of the oldest running publish step by step. Absent when none exist. |
| `imgsrv_publish_versions_publishing` | Gauge | none | Image versions currently in `publishing` state. |
| `imgsrv_incus_projection_rows` | Gauge | none | Current Incus Simple Streams projection rows. |

### Background Jobs

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `imgsrv_background_job_attempts_total` | Counter | `job`, `outcome` | Background job attempts. `outcome` is `worked`, `idle`, or `error`. |
| `imgsrv_background_job_errors_total` | Counter | `job` | Background job errors. |
| `imgsrv_background_job_circuit_open_total` | Counter | `job` | Circuit breaker openings. |
| `imgsrv_background_job_consecutive_failures` | Gauge | `job` | Current consecutive failure count. |
| `imgsrv_background_job_last_success_timestamp_seconds` | Gauge | `job` | Unix timestamp of the last successful attempt. Idle attempts count as successful attempts. |
| `imgsrv_background_job_last_error_timestamp_seconds` | Gauge | `job` | Unix timestamp of the last failed attempt. |

The built-in job labels are `cas-promotion` and `publish`.

## Starter Alerts

These examples are intentionally conservative starting points. Tune thresholds
to match the deployment and workload.

| Condition | Example PromQL |
| --- | --- |
| Postgres pool saturated | `imgsrv_postgres_pool_connections{state="acquired"} / imgsrv_postgres_pool_connections{state="max"} > 0.8` |
| Object-store errors increasing | `sum(rate(imgsrv_objectstore_operations_total{outcome!="success"}[5m])) > 0` |
| CAS ingest queue stuck | `imgsrv_cas_ingest_oldest_queued_age_seconds > 300` |
| CAS ingest worker stuck running | `imgsrv_cas_ingest_oldest_running_age_seconds > 900` |
| Publish step queue stuck | `max by (step) (imgsrv_publish_step_oldest_queued_age_seconds) > 300` |
| Background job circuit breaker opened | `increase(imgsrv_background_job_circuit_open_total[10m]) > 0` |

## Resource attributes

Every series carries the OpenTelemetry resource attributes set by the process:

- `service.name` — `imgsrv` (fixed).
- `service.version` — release version when the binary was built with linker
  metadata; otherwise absent.

## Disabling metrics

Set `IMGSRV_METRICS_LISTEN` or `--metrics-listen` to an empty value to disable
the listener entirely.
