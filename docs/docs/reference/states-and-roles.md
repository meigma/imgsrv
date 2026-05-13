---
title: States and roles
description: State machines, built-in roles, and the role-to-endpoint matrix.
sidebar_position: 3
---

# States and roles

This page is a lookup for the small structured facts that surface across the
API: upload session states, image version states, publish job and step states,
publish step order, and the built-in role matrix.

For the wire contract, see the OpenAPI specification at
[`/openapi/v1.yaml`](pathname:///openapi/v1.yaml).

## Upload sessions

An upload session moves through this lifecycle. State transitions are durable
and survive process restart.

| State | Meaning |
| --- | --- |
| `created` | Session exists; no parts accepted. |
| `uploading` | At least one part has been accepted. |
| `completed` | Object storage has finalized the multipart object into staging. |
| `ingesting` | A CAS-promotion worker is verifying and promoting the staged bytes. |
| `ready` | The expected digest exists as a verified CAS blob. |
| `failed` | CAS promotion failed for this session. |
| `aborted` | The session was aborted before promotion started, or expired. |

Re-uploading the same part number before `completed` replaces the recorded
part. Completing an upload that is already `completed`, `ingesting`, or `ready`
returns the current state rather than creating a second staged object.

## Image versions

A version is operator-controlled while in `draft` and immutable from `published`.

| State | Meaning |
| --- | --- |
| `draft` | Manifest can still be edited; artifacts and attachments may be added or removed. |
| `publishing` | Manifest is frozen and durable publish steps are running. |
| `published` | Manifest is immutable. |

## Publish jobs

A publish job is the durable workflow that promotes a `draft` version to
`published`.

| Job state | Meaning |
| --- | --- |
| `queued` | No publish step has started. |
| `running` | At least one publish step is running or has run. |
| `succeeded` | All blocking steps completed; the version is `published`. |
| `failed` | A blocking step failed. Retry with `POST /v1/publish-jobs/{job_id}/retry`. |

Each job runs an ordered list of steps. A step has its own lifecycle:

| Step state | Meaning |
| --- | --- |
| `queued` | Waiting to be claimed by a worker. |
| `running` | A worker has claimed the step under a lease. |
| `succeeded` | The step completed successfully. |
| `failed` | The step failed and recorded an operator-visible reason. |
| `skipped` | The step was intentionally skipped. |

### Publish step order

| Position | Step | Effect |
| --- | --- | --- |
| 1 | `validate_catalog` | Verify publish preconditions against durable catalog state and confirm referenced CAS blobs are trusted. |
| 2 | `incus_index` | Compute and persist Incus Simple Streams projection rows for eligible artifacts. |
| 3 | `finalize_publish` | Transition the version from `publishing` to `published`. |

Retrying a failed job requeues from the first failed blocking step.

## Built-in roles

| Role | Action it grants | Scope |
| --- | --- | --- |
| `auth-manager` | `auth.manage` | Auth-management operations on the `auth` resource. |
| `content-writer` | `content.write` | Upload, draft editing, publishing, publish-job retry, and alias mutation on the `content` resource. |

A principal may hold zero, one, or both roles. Both roles can be assigned to a
single service principal where a tightly coupled deployment publishes content
and manages auth from the same identity.

## Endpoint matrix

Write endpoints that require `content.write` (i.e. the `content-writer` role):

```
POST   /v1/uploads
PUT    /v1/uploads/{upload_id}/parts/{part_number}
POST   /v1/uploads/{upload_id}/complete
POST   /v1/uploads/{upload_id}/abort
POST   /v1/images
POST   /v1/images/{name}/versions
POST   /v1/images/{name}/versions/{version}/artifacts
DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}
POST   /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments
DELETE /v1/images/{name}/versions/{version}/artifacts/{artifact_id}/attachments/{attachment_id}
POST   /v1/images/{name}/versions/{version}/publish
GET    /v1/publish-jobs/{job_id}
POST   /v1/publish-jobs/{job_id}/retry
PUT    /v1/images/{name}/aliases/{alias}
DELETE /v1/images/{name}/aliases/{alias}
```

Endpoints that require `auth.manage` (i.e. the `auth-manager` role):

```
GET    /v1/auth/roles
GET    /v1/auth/principals
POST   /v1/auth/principals
GET    /v1/auth/principals/{principal_id}
PUT    /v1/auth/principals/{principal_id}/roles/{role_id}
DELETE /v1/auth/principals/{principal_id}/roles/{role_id}
POST   /v1/auth/principals/{principal_id}/api-tokens
GET    /v1/auth/principals/{principal_id}/api-tokens
DELETE /v1/auth/api-tokens/{token_id}
GET    /v1/auth/oidc-provisioning-rules
POST   /v1/auth/oidc-provisioning-rules
GET    /v1/auth/oidc-provisioning-rules/{rule_id}
PUT    /v1/auth/oidc-provisioning-rules/{rule_id}
DELETE /v1/auth/oidc-provisioning-rules/{rule_id}
POST   /v1/auth/oidc-provisioning-rules/{rule_id}/reconciliation
```

Read endpoints (image catalog, manifests, downloads, Simple Streams documents,
and operational `/healthz`/`/readyz`) are unauthenticated in v0.
