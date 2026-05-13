//go:build integration

package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"

	"github.com/meigma/imgsrv/internal/objectstore/s3"
)

const (
	postgresIdentifierMaxBytes = 63
	uniqueSuffixBytes          = 8
)

// Dependencies owns the expensive external services shared by integration environments.
type Dependencies struct {
	postgresURL       string
	postgresContainer testcontainers.Container
	s3Config          s3.Config
	garageContainer   testcontainers.Container
	garageTempDir     string
}

// StartDependencies starts the shared Postgres and Garage containers.
func StartDependencies(ctx context.Context) (*Dependencies, error) {
	postgresURL, postgresContainer, err := startPostgresContainer(ctx)
	if err != nil {
		return nil, errors.Join(err, terminateContainer(ctx, postgresContainer))
	}

	s3Config, garageContainer, garageTempDir, err := startGarageContainer(ctx)
	if err != nil {
		return nil, errors.Join(
			err,
			terminateContainer(ctx, garageContainer),
			removeTempDir(garageTempDir),
			terminateContainer(ctx, postgresContainer),
		)
	}

	return &Dependencies{
		postgresURL:       postgresURL,
		postgresContainer: postgresContainer,
		s3Config:          s3Config,
		garageContainer:   garageContainer,
		garageTempDir:     garageTempDir,
	}, nil
}

// Close terminates the shared dependency containers.
func (deps *Dependencies) Close(ctx context.Context) error {
	if deps == nil {
		return nil
	}

	return errors.Join(
		terminateContainer(ctx, deps.garageContainer),
		terminateContainer(ctx, deps.postgresContainer),
		removeTempDir(deps.garageTempDir),
	)
}

func terminateContainer(ctx context.Context, container testcontainers.Container) error {
	if container == nil {
		return nil
	}
	if err := container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate container %s: %w", container.GetContainerID(), err)
	}

	return nil
}

func removeTempDir(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove temp dir %s: %w", path, err)
	}

	return nil
}

type isolationNames struct {
	schema       string
	objectPrefix string
}

func newIsolationNames(t testing.TB) isolationNames {
	t.Helper()

	token := uniqueTestToken(t.Name())

	return isolationNames{
		schema:       schemaName(token),
		objectPrefix: "tests/" + token + "/",
	}
}

func uniqueTestToken(testName string) string {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:uniqueSuffixBytes]
	safe := safeName(testName)
	budget := postgresIdentifierMaxBytes - len("imgsrv_test") - len(suffix) - 2
	if len(safe) > budget {
		safe = safe[:budget]
	}

	return safe + "_" + suffix
}

func schemaName(token string) string {
	return "imgsrv_test_" + token
}

func safeName(name string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, value := range strings.ToLower(name) {
		allowed := value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
		if allowed {
			builder.WriteRune(value)
			lastUnderscore = false
			continue
		}
		if lastUnderscore {
			continue
		}
		builder.WriteByte('_')
		lastUnderscore = true
	}

	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "test"
	}

	return result
}
