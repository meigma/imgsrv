# imgsrv

`imgsrv` is a Go HTTP service for storing, cataloging, publishing, and serving
disk and VM image artifacts. It gives operators a native API for verified
content-addressed uploads, immutable image versions, movable aliases, proxied
artifact downloads, API-token and OIDC publisher auth, and an Incus-compatible
Simple Streams read surface.

## Quick Start

### Prerequisites

- Go 1.26
- PostgreSQL for the control plane
- S3-compatible object storage for image bytes

### Verify The Repository

```sh
go test ./...
```

### Run The Server

Start the server with only operational endpoints:

```sh
go run ./cmd/imgsrv --listen :8080
```

That process serves:

- `GET /healthz`
- `GET /readyz`
- Prometheus metrics on `127.0.0.1:9464/metrics`

Set `--metrics-listen ""` to disable the metrics server.

Run the API with PostgreSQL and S3-compatible object storage:

```sh
IMGSRV_POSTGRES_URL='postgres://imgsrv:imgsrv@localhost:5432/imgsrv?sslmode=disable' \
IMGSRV_S3_ENDPOINT='127.0.0.1:3900' \
IMGSRV_S3_BUCKET='imgsrv' \
IMGSRV_S3_ACCESS_KEY_ID='imgsrv' \
IMGSRV_S3_SECRET_ACCESS_KEY='imgsrv-secret' \
IMGSRV_S3_PATH_STYLE=true \
IMGSRV_CAS_PROMOTION_ENABLED=true \
go run ./cmd/imgsrv --listen :8080
```

At startup, `imgsrv` applies embedded PostgreSQL migrations. A fresh
PostgreSQL-backed deployment with no `auth-manager` principal prints one
bootstrap API token to stdout. Use that token to create service principals,
assign roles, issue API tokens, and configure OIDC provisioning rules through
the `/v1/auth/*` API.

## Server Usage

Write operations require a bearer principal with the right role:

- `auth-manager` manages principals, local roles, API tokens, and OIDC
  provisioning rules.
- `content-writer` creates uploads, edits draft versions, publishes versions,
  retries failed publish jobs, and manages aliases.

The normal publishing flow is:

1. Upload bytes into CAS with `/v1/uploads`.
2. Create an image and draft version under `/v1/images`.
3. Attach primary artifacts and any metadata attachments by CAS digest.
4. Publish the draft version and track `/v1/publish-jobs/{job_id}`.
5. Move aliases with `/v1/images/{name}/aliases/{alias}`.
6. Read published artifacts through `/v1/images/*` or the Incus Simple Streams
   documents under `/streams/v1/`.

The OpenAPI contract for the implemented HTTP surface is
[docs/static/openapi/v1.yaml](docs/static/openapi/v1.yaml).

## Documentation

The published documentation is available at
[meigma.github.io/imgsrv](https://meigma.github.io/imgsrv/). The source lives
under [docs/docs](docs/docs).

## Support

Use [GitHub Discussions](https://github.com/meigma/imgsrv/discussions) for
questions and design discussion.
Use [GitHub Issues](https://github.com/meigma/imgsrv/issues) for non-security
bug reports and scoped feature requests.
Do not report vulnerabilities in public channels. See [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines, local setup
expectations, and pull request workflow.

## Security

See [SECURITY.md](SECURITY.md) for supported versions and private vulnerability
reporting.

## License

`imgsrv` is dual-licensed under either:

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))
- MIT license ([LICENSE-MIT](LICENSE-MIT))

at your option.
