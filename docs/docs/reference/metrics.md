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

## Series

Application-level metrics are not yet emitted. The only series currently on the
endpoint come from the OpenTelemetry HTTP instrumentation wrapping the public
API listener.

| Series | Type | Labels | Description |
| --- | --- | --- | --- |
| `http_server_request_duration_seconds` | Histogram | `http_request_method`, `http_response_status_code`, `http_route`, `network_protocol_name`, `server_address`, `server_port`, `url_scheme` | Per-request server-side latency in seconds. Routes are reported using the matched `http.ServeMux` pattern, so values look like `GET /v1/images/{name}`. |
| `http_server_active_requests` | UpDownCounter | `http_request_method`, `url_scheme`, `server_address`, `server_port` | Number of in-flight requests on the public API listener. |

Exact label sets follow the OpenTelemetry
[HTTP semantic conventions](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/);
any addition or revision the upstream conventions make appears here without an
imgsrv code change.

## Resource attributes

Every series carries the OpenTelemetry resource attributes set by the process:

- `service.name` — `imgsrv` (fixed).
- `service.version` — release version when the binary was built with linker
  metadata; otherwise absent.

## Disabling metrics

Set `IMGSRV_METRICS_LISTEN` or `--metrics-listen` to an empty value to disable
the listener entirely.
