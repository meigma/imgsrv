# imgsrvtest

`github.com/meigma/imgsrv/test` is an exported test harness for downstream
consumers of the imgsrv API.

Use this package from another project when that project wants to start a
disposable imgsrv environment for its own functional or integration tests. The
package owns convenience wiring for server lifecycle, dependencies, API tokens,
CAS promotion, and SDK client construction.

This package is not imgsrv's own integration test suite. Repository-owned API
behavior coverage belongs under `internal/integration`, where tests can assert
imgsrv internals and adapter behavior directly.

Do not add imgsrv regression tests or endpoint coverage tests here. Add only
exported harness helpers that external consumers can reasonably use.

The package is guarded by the `integration` build tag, so callers must run tests
that import it with `-tags=integration`.
