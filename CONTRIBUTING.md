# Contributing

Thank you for your interest in contributing to `imgsrv`.
This repository is early-stage, so keep changes focused and bias toward small
working slices over large speculative designs.

For private vulnerability reporting, use [SECURITY.md](SECURITY.md) instead of
public channels.

## Asking Questions

Use [GitHub Discussions](https://github.com/meigma/imgsrv/discussions) for usage
questions, troubleshooting, and design discussion.

## Reporting Bugs

Report non-security bugs through [GitHub Issues](https://github.com/meigma/imgsrv/issues).
Include the following details when possible:

- version, commit, or environment details
- steps to reproduce
- expected behavior
- actual behavior
- logs, screenshots, or a minimal reproduction

If you are reporting a security issue, stop and follow [SECURITY.md](SECURITY.md)
instead.

## Proposing Features

Use [GitHub Discussions](https://github.com/meigma/imgsrv/discussions) for broad
design discussion and [GitHub Issues](https://github.com/meigma/imgsrv/issues)
for scoped, actionable feature requests.

For larger changes, describe the problem, the proposed approach, and any
compatibility or migration concerns before starting implementation.

## Pull Requests

Contributors should:

1. Keep changes focused and scoped to a single problem.
2. Add or update tests when behavior changes.
3. Update documentation when user-facing behavior changes.
4. Describe the change clearly in the pull request.
5. Make sure CI passes before requesting review.

## Local Setup

Install docs dependencies:

```sh
npm --prefix docs ci
```

Useful project commands:

```sh
moon ci --summary minimal
npm --prefix docs run build
npm --prefix docs run typecheck
```

The Go service module has not been added yet. Add service build and test
commands here when the first implementation slice lands.

## License

Unless otherwise stated, contributions are accepted under the repository's
dual Apache-2.0 OR MIT license.
