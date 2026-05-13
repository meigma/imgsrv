---
title: Consume images
description: Download published artifacts and subscribe Incus to the Simple Streams projection.
sidebar_position: 2
---

# Consume images

Reading published image data from `imgsrv` is unauthenticated in v0.1.
Consumers reach published artifacts through two surfaces: the native HTTP API
and the Incus Simple Streams projection.

For the wire contract, see [`/openapi/v1.yaml`](pathname:///openapi/v1.yaml).

## Native HTTP API

### List images and versions

```bash
curl -sf "$IMGSRV/v1/images" | jq
curl -sf "$IMGSRV/v1/images/debian-bookworm/versions" | jq
```

### Download a primary artifact

A published version's manifest enumerates its artifacts. Each artifact has a
stable `artifact_id` for use in the download URL:

```bash
curl -sf "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0" | jq '.artifacts[]'

curl -fL -o image.qcow2 \
  "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0/artifacts/$ARTIFACT_ID/download"
```

Downloads stream through `imgsrv`. There are no expiring URLs and no
redirects to S3-shaped hosts.

### Download an attachment

Attachments share the same URL shape:

```bash
curl -fL -o image.tar.xz \
  "$IMGSRV/v1/images/debian-bookworm/versions/12.5.0/artifacts/$ARTIFACT_ID/attachments/$ATTACHMENT_ID/download"
```

### Resolve an alias

An alias points to a published version. The consumer is free to resolve the
alias first and then download from the resulting version:

```bash
curl -sf "$IMGSRV/v1/images/debian-bookworm/aliases/latest" \
  | jq -r '.version'
```

Alias resolutions are evaluated at request time; a consumer that follows
`latest` always lands on whatever version the operator most recently pointed
it at.

## Incus Simple Streams

`imgsrv` projects eligible published artifacts into the Incus Simple Streams
format. Add the service as an Incus remote and consume images using normal
`incus` commands:

```bash
incus remote add imgsrv "$IMGSRV" --protocol=simplestreams
incus image list imgsrv:
incus launch imgsrv:debian-bookworm/12.5.0 my-instance
```

The Simple Streams documents are served at:

- `GET /streams/v1/index.json` — index document.
- `GET /streams/v1/images.json` — image product file.

### Eligibility

An artifact appears in Simple Streams when:

- Its version is `published`.
- The artifact's format is `qcow2`.
- The artifact has an attachment named `incus.tar.xz`.

Aliases are resolved live when the Simple Streams documents render, so a
consumer who follows an alias-shaped Incus remote sees alias movement
without any change on their side.

For the operator side of producing Simple Streams-eligible releases, see
[Publish image versions](./publish-image-versions.md#simple-streams-eligibility).

## Immutability guarantees a consumer can rely on

- A published version's manifest does not change after the publish job
  reaches `succeeded`. A version reference is stable for the lifetime of the
  service deployment.
- A primary artifact's `blob_digest` matches the bytes served by its
  download URL, as long as the deployment trust boundary holds. See
  [Trust model](../concepts/trust-model.md) for the limits.
- An alias is mutable. A consumer who needs a fixed target must depend on a
  version string, not on an alias.
