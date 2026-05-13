---
title: Publishing model
description: How imgsrv separates content from meaning, and why publishing is the immutability boundary.
sidebar_position: 1
---

# Publishing model

`imgsrv` separates two concerns that registries and image stores tend to
conflate: the bytes of an image and the meaning those bytes carry for a
release. The publishing model is the seam between them.

## Content and meaning

A CAS blob is a verified object addressed by `sha256:<hex>`. Once a blob is
trusted, it is content — nothing more. Two image versions that ship the same
qcow2 share the same blob. The blob does not know which image it belongs to,
which release it ships with, or which operating system it represents.

An image version supplies that context. A version's manifest lists release
artifacts, each of which references one primary CAS blob and records the
metadata that gives the blob meaning: operating system, architecture, format,
media type, declared size, declared digest. Attachments hang off artifacts and
let secondary blobs — signatures, SBOMs, vendor metadata, Incus-specific
metadata — travel alongside without leaking into the core artifact schema.

This split is why `imgsrv` looks more like a small OCI-style registry than a
file server. Bytes are dedup'd and verified once; meaning lives in
version-scoped manifests that can be reviewed, frozen, and immutably published.

## Drafts as edit-space

A new version starts as a `draft`. The draft is operator-controlled: artifacts
can be added or removed, attachments can be attached to artifacts, and CAS
blobs can be cited before they are trusted. The draft is the place where a
release manifest is composed.

Drafts solve a small but real problem. Uploading raw bytes and asserting "this
release is now real" cannot be the same operation, because bytes arrive at
upload time and meaning arrives at human-decision time. Drafts let the two
arrive independently and converge at publish.

## Publishing as the immutability boundary

The publish operation is the line in the model. Before publish, the manifest
is mutable. After publish, the manifest cannot change. A `publishing` version
sits in between: the manifest is frozen and durable publish steps are running.

Publishing is also where validation runs. The publish job's first step,
`validate_catalog`, walks the frozen manifest and refuses to proceed if any
referenced primary blob or attachment is not trusted in CAS. A version that
cites a digest that has not been uploaded and verified will fail publish, not
publish with a broken pointer.

The publish job is durable because publishing is more than a state transition.
The current pipeline runs three steps in order:

1. `validate_catalog` — verify preconditions against catalog state.
2. `incus_index` — compute and persist Incus Simple Streams projection rows
   for eligible artifacts.
3. `finalize_publish` — mark the version as `published`.

Each step records its own state, holds a worker lease while running, and can
be retried after a failure. A publish that fails on the indexer can be retried
from that step after the underlying problem is fixed, without re-running
validation. The full state lookup lives in
[States and roles](../reference/states-and-roles.md#publish-jobs).

## Aliases as mutable pointers

A version string is a stable, immutable handle to a specific manifest. That is
useful for reproducibility but inconvenient for "the latest stable thing." An
alias is the answer: a named pointer scoped to an image, pointing at one
published version, freely movable to another published version.

Aliases never point at drafts, and versions themselves never move. `latest`
might point to `v1.2.0` today and `v1.2.1` tomorrow, but `v1.2.0` and `v1.2.1`
remain exactly the manifests they were at publish time. Consumers who want
freshness depend on alias names; consumers who want reproducibility depend on
version strings or digests.

## What this model is not

The publishing model is not a layer system. Versions are not deltas, and a
publish does not produce a derived artifact. The model is also not a
fully-trusted ledger; published immutability is enforced by `imgsrv` and
PostgreSQL, not by signatures over append-only logs. The boundaries of that
trust are covered in [Trust model](./trust-model.md).
