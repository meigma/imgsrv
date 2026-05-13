---
title: Operate imgsrv
description: Day-2 operations — health checks, monitoring, backups, upgrades, and publish-job triage.
sidebar_position: 3
---

# Operate imgsrv

This page is the day-2 reference: what to wire into a load balancer, what to
alert on, how to triage failed publish jobs, how to back up and upgrade the
service.

## Health and readiness

`imgsrv` exposes two operational endpoints on the API listener:

| Endpoint | Returns | Use |
| --- | --- | --- |
| `GET /healthz` | `204 No Content` unconditionally | Liveness probe. Indicates the HTTP server is accepting requests. |
| `GET /readyz` | `204` when ready, `503` otherwise | Readiness probe. Currently has no backing-service readiness check wired by default, so it returns `204` whenever the process is up. |

Wire `/healthz` into liveness probes (Kubernetes, ECS, the load balancer's
health check). Wire `/readyz` into readiness probes; the response shape is
already correct for the day a backing-service readiness check is added,
without rewiring deployments.

## Metrics and alerting

Metrics are served on the metrics listener (default `127.0.0.1:9464/metrics`),
formatted for Prometheus and OpenMetrics. See [Metrics](../reference/metrics.md)
for the exact series.

The application currently emits only the OpenTelemetry HTTP semantic-convention
series for the public API listener. The minimum useful alerts:

- **Elevated request error rate**: `http_response_status_code` ≥ 500 on the
  public API listener for more than a few minutes.
- **Sustained high latency**: p99 of `http_server_request_duration_seconds`
  above the application's SLO for sustained windows.
- **Probe failure**: load-balancer reports of `/readyz` failing for more
  than one consecutive interval.

Alert on the absence of metrics too: a scrape gap longer than the alerting
window often signals a stuck process before any latency signal fires.

## Triage a failed publish job

A publish job fails when one of its ordered steps fails. The publisher who
issued the publish sees the failure as a non-success terminal state in
`GET /v1/publish-jobs/{job_id}`. The job's response carries the failing step
name and the operator-visible reason.

Common failure modes and the action they need:

| Failing step | Likely cause | Action |
| --- | --- | --- |
| `validate_catalog` | A referenced CAS digest is not trusted (upload not completed or promotion failed) | Verify upload sessions reached `ready`; promote any stuck `completed`/`ingesting` sessions before retrying. |
| `incus_index` | Object-store or database error during projection write | Investigate object-store and database health; retry once the underlying issue clears. |
| `finalize_publish` | Database transaction failure | Investigate database health; retry. |

After fixing the underlying issue, retry the job. The retry requeues from the
first failed blocking step — completed earlier steps are not re-run:

```bash
curl -sf -X POST \
  "https://imgsrv.example.com/v1/publish-jobs/$JOB_ID/retry" \
  -H "Authorization: Bearer $CONTENT_WRITER_TOKEN"
```

If a job cannot be made to succeed (for example, the manifest itself is
wrong), the version stays in `publishing` indefinitely. There is no in-band
"abandon publish" today; the failing state is durable and visible. A future
cleanup path may add it.

## Backups

Two stores need consistent backups: the PostgreSQL control plane and the
S3-compatible object store. Restore guarantees depend on both being captured
together.

Recommended order:

1. **Pause publish workers.** Either stop the `imgsrv` process or run
   `imgsrv` with `--cas-promotion-enabled=false`. Halting writes for the
   duration of the snapshot is the simplest way to keep the two stores
   consistent.
2. **Snapshot object storage first.** Use the provider's snapshot or
   versioned-bucket feature. Garage exposes per-bucket snapshots through
   `garage`.
3. **Snapshot PostgreSQL.** `pg_dump` against the database, or a logical
   backup tool such as `pgBackRest`. The database is small relative to the
   object store and the dump is fast.
4. **Resume publish workers.**

The order matters. If the database is snapshotted first and then a CAS object
lands between snapshots, the manifest references a blob the restored bucket
does not have. Snapshotting the object store first and then the database
produces a database that may reference only blobs already present.

## Upgrade

`imgsrv` applies embedded migrations at startup. To upgrade across versions:

1. Read the release notes for migration warnings. Most releases apply
   in-place; some may require downtime.
2. Snapshot PostgreSQL.
3. Drain the existing process. For Kubernetes-style deployments, the
   graceful termination flow (`SIGTERM` followed by the
   `--shutdown-timeout` window) is sufficient.
4. Start the new binary or image. Startup applies pending migrations
   before the listener becomes ready.

Rolling deploys are safe when migrations are backward-compatible. The
release notes call out any migration that requires draining before the new
version starts.

## CAS-promotion worker tuning

The CAS-promotion worker verifies completed upload sessions and records the
trusted CAS blob. Tuning matters under load.

- `--cas-promotion-poll-interval` (default `5s`): how often the worker
  checks for completed sessions when idle. Lower it under sustained upload
  rate; raise it in mostly-idle deployments to reduce database polling.
- `--cas-promotion-error-backoff` and `--cas-promotion-error-backoff-max`:
  the worker backs off on failure, capped by the max value. Defaults
  (`5s` → `1m`) are reasonable for transient object-store hiccups.
- `--cas-promotion-circuit-breaker-failures` (default `10`) and
  `--cas-promotion-circuit-breaker-cooldown` (default `1m`): when the
  failure count reaches the threshold, the breaker opens for the cooldown
  window before another attempt. Raise the cooldown when the upstream
  failure is persistent (an object-store outage); lower it for flaky
  short-duration failures.

A persistent failure in CAS promotion stops uploads from reaching `ready`,
which blocks publishes that reference them. The HTTP error rate and the
upload-session state distribution are the two operational signals that show
the worker is unhealthy.

## When to consult other docs

- Flag and environment reference: [Configuration](../reference/configuration.md).
- State machines: [States and roles](../reference/states-and-roles.md).
- Auth lifecycle (principals, OIDC rules, recovery): [Manage authentication](./manage-auth.md).
- Concept frame for what publishing actually does: [Publishing model](../concepts/publishing-model.md).
