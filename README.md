# imgsrv

`imgsrv` is an early-stage Go service for storing, cataloging, and serving disk
and VM image artifacts through a native HTTP API.

The v0 prototype is intended to prove verified content-addressed uploads,
immutable published image versions, aliases, and proxied downloads backed by
PostgreSQL and Garage. The initial service foundation is in place; the current
implementation exposes operational health endpoints while the v0 API is built
out in focused slices.

## Quick Start

Current repository commands exercise the documentation site and initial Go
service foundation.

### Prerequisites

- Node.js 22, matching [.nvmrc](.nvmrc)
- npm
- Go 1.26
- Moon
- uv, for repository setup automation
- gh, authenticated with access to `meigma/imgsrv`

### Setup

```sh
npm --prefix docs ci
```

### Verify

```sh
moon ci --summary minimal
go test ./...
```

### Run Docs Locally

```sh
npm --prefix docs run start
```

### Run Service Locally

```sh
go run ./cmd/imgsrv --listen :8080
```

The initial service foundation exposes `GET /healthz` and `GET /readyz`.
Logs default to text output and can be switched to JSON with `--log-format json`;
verbosity is controlled with `--verbosity`. Prometheus metrics are served from
`127.0.0.1:9464/metrics` by default and can be disabled with
`--metrics-listen ""`.

Set `--postgres-url` or `IMGSRV_POSTGRES_URL` to open PostgreSQL at startup and
apply embedded Goose migrations before the HTTP listener starts.

## Planned Service Shape

The service is planned as a single Go binary using standard-library HTTP,
PostgreSQL for the control plane, Garage through its S3-compatible API for
object storage, `log/slog` for logging, OpenTelemetry metrics with a Prometheus
endpoint, and a hand-written OpenAPI v3 specification.

See [docs/docs/design.md](docs/docs/design.md) for the current working design.

## Documentation

- Docs home: [docs/docs/index.md](docs/docs/index.md)
- Working v0 design: [docs/docs/design.md](docs/docs/design.md)
- Repository settings manifest: [.github/repository-settings.toml](.github/repository-settings.toml)

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
