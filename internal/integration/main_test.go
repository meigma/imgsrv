//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/meigma/imgsrv/internal/integration/harness"
)

var sharedDependencies *harness.Dependencies

func TestMain(m *testing.M) {
	ctx := context.Background()
	deps, err := harness.StartDependencies(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start integration dependencies: %v\n", err)
		os.Exit(1)
	}
	sharedDependencies = deps

	code := m.Run()
	if err := deps.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "close integration dependencies: %v\n", err)
		if code == 0 {
			code = 1
		}
	}

	os.Exit(code)
}

func startEnv(t testing.TB, opts ...harness.Option) *harness.Env {
	t.Helper()
	if sharedDependencies == nil {
		t.Fatal("integration dependencies are not started")
	}

	return harness.StartWithDependencies(t, sharedDependencies, opts...)
}

func startIntegrationEnv(t testing.TB) *harness.Env {
	t.Helper()

	return startEnv(t, harness.WithAPIToken())
}
