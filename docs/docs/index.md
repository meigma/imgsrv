---
title: imgsrv
slug: /
description: Documentation for the imgsrv image artifact service.
sidebar_position: 1
---

# imgsrv

`imgsrv` is an HTTP service for storing, cataloging, publishing, and serving
disk and VM image artifacts. It pairs verified content-addressed uploads with
immutable image versions, movable aliases, proxied artifact downloads, local
and OIDC publisher auth, and an Incus-compatible Simple Streams read surface.

Start where your role fits.

## Run imgsrv

Documentation for the people who deploy and operate the service.

- [Deploy imgsrv](./operators/deploy.md) — production deployment against
  PostgreSQL and S3-compatible object storage.
- [Manage authentication](./operators/manage-auth.md) — bootstrap principals,
  rotate API tokens, configure OIDC publishers.
- [Operate imgsrv](./operators/operate.md) — health, metrics, backups,
  upgrades, and triage of failed publish jobs.

## Publish images

Documentation for the people whose CI or release tooling pushes images.

- [Publish image versions](./publishers/publish-image-versions.md) — the
  canonical immutable release flow.
- [Consume images](./publishers/consume-images.md) — read published artifacts
  and subscribe Incus to the Simple Streams projection.

## Other entry points

- [Publish your first image](./tutorial/publish-your-first-image.md) — an
  end-to-end guided walkthrough, useful first read for either role.
- [Concepts](./concepts/publishing-model.md) — the model behind the system:
  publishing, auth, and the trust boundary.
- [Reference](./reference/configuration.md) — flags, metrics, state machines,
  and the role-to-endpoint matrix.
- [OpenAPI v1](pathname:///openapi/v1.yaml) — the wire contract.

## Repository

- [Source](https://github.com/meigma/imgsrv)
- [Contributing](https://github.com/meigma/imgsrv/blob/master/CONTRIBUTING.md)
- [Security](https://github.com/meigma/imgsrv/blob/master/SECURITY.md)
