---
title: Trust model
description: What imgsrv guarantees about immutability and where operator trust enters.
sidebar_position: 3
---

# Trust model

`imgsrv` makes specific guarantees about how published content behaves. This
page describes what those guarantees are, how they are enforced, and where the
trust boundary actually sits.

## What "immutable" means here

A published image version's manifest does not change through normal service
paths. The guarantee is enforced by three reinforcing mechanisms:

- **PostgreSQL constraints** prevent direct modification of published rows.
  The catalog schema rejects edits to a `published` version's artifacts,
  attachments, and metadata.
- **Narrow write paths**. The publish handler is the only code that
  transitions a version into and through the publish lifecycle. Once a version
  reaches `published`, no handler in the codebase will move it back.
- **Digest-addressed CAS keys**. CAS objects in object storage are stored
  under keys derived from their `sha256:<hex>` identity. The application does
  not write a different blob to an existing CAS key, so a manifest's reference
  to `sha256:abc...` will continue to address the same bytes.

These mechanisms produce best-effort immutability for the service path. They
are sufficient for the typical operational threat model where the question is
"can a misconfigured client or a buggy job accidentally rewrite a published
release?" and the answer is no.

## Where the boundary sits

The trust boundary stops at the privileged infrastructure. An operator with
direct access to PostgreSQL can update rows that the service refuses to
modify. An operator with direct write access to the object store can overwrite
a CAS object with different bytes; the digest stamped on the manifest will
still match the original, but the bytes served behind it will not.

`imgsrv` does not attempt to defend against either case. A deployment should
treat the database and object store as privileged infrastructure on par with
the keys to the kingdom — protected with the same care as other production
secrets, audited the same way, and accessed through the same break-glass
process as any other primary datastore.

## Why downloads are proxied

Published artifact downloads flow through `imgsrv` rather than through
pre-signed object-store URLs. This is a trust decision, not just an
implementation convenience:

- Clients consume stable service URLs. The object store remains an internal
  implementation detail and can be moved or reconfigured without breaking
  consumer URLs already baked into Incus remotes or scripts.
- No expiring or signed-URL behavior leaks to clients. A `GET` on a published
  artifact never returns a redirect to an S3 host with a query-string
  signature.
- The service is the single point where access checks could be added later.
  Authenticated downloads, private images, or per-version policy can land
  inside the existing handler without revisiting client expectations.

## What v0 does not yet provide

The current guarantees are best-effort within the service trust boundary. Two
deferred directions would extend them:

- A transparency-log integration would let a published manifest be witnessed
  by an external append-only log, so that any tampering inside the service
  trust boundary becomes externally detectable. This is intentionally not
  built in v0.
- Signed publication records would let consumers verify that a manifest came
  from a particular publisher without trusting the runtime path. The current
  release surface relies on the operator-controlled identity matrix described
  in [Auth model](./auth-model.md).

These are not promised for v1, and their absence does not undermine the
service-path immutability guarantees described above. They are the natural
next step when a deployment's threat model expands past the assumption of a
trusted operator.
