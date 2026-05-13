//go:build integration

package harness

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meigma/imgsrv/internal/objectstore"
	"github.com/meigma/imgsrv/internal/objectstore/s3"
)

const (
	// garageImage is the Garage container image used for the integration harness.
	garageImage = "dxflrs/garage:v2.3.0"

	// garageS3Port is the container port serving the S3-compatible API.
	garageS3Port = "3900/tcp"

	// garageRegion is the S3 region advertised by the Garage instance.
	garageRegion = "garage"

	// garageBucket is the bucket pre-created for integration tests.
	garageBucket = "imgsrv-integration"

	// garageAccessKeyID is the static access key seeded into the container.
	garageAccessKeyID = "GK00000000000000000000000000000000"

	// garageSecretAccessKey is the static secret key seeded into the container.
	garageSecretAccessKey = "0000000000000000000000000000000000000000000000000000000000000000"

	// garageStartupTimeout bounds how long the harness waits for the container
	// to begin listening on its S3 port.
	garageStartupTimeout = time.Minute

	// garageReadyTimeout bounds how long the harness waits for the default
	// bucket to become reachable.
	garageReadyTimeout = 30 * time.Second

	// garageReadyInterval is the polling interval used while waiting for the
	// default bucket to appear.
	garageReadyInterval = 100 * time.Millisecond

	// garageConfigFileMode is the file mode for the rendered garage.toml on
	// both the host and inside the container.
	garageConfigFileMode = 0600
)

// startGarage launches a single-node Garage container, waits for the default
// bucket to be reachable, and returns the S3 configuration tests should use.
func startGarage(ctx context.Context, t testing.TB) s3.Config {
	t.Helper()

	config, container, tempDir, err := startGarageContainer(ctx)
	if tempDir != "" {
		t.Cleanup(func() {
			require.NoError(t, os.RemoveAll(tempDir))
		})
	}
	if container != nil {
		testcontainers.CleanupContainer(t, container)
	}
	require.NoError(t, err)

	return config
}

func startGarageContainer(ctx context.Context) (s3.Config, testcontainers.Container, string, error) {
	configPath, tempDir, err := writeGarageConfig()
	if err != nil {
		return s3.Config{}, nil, tempDir, err
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        garageImage,
			ExposedPorts: []string{garageS3Port},
			Env: map[string]string{
				"GARAGE_DEFAULT_ACCESS_KEY": garageAccessKeyID,
				"GARAGE_DEFAULT_SECRET_KEY": garageSecretAccessKey,
				"GARAGE_DEFAULT_BUCKET":     garageBucket,
			},
			Files: []testcontainers.ContainerFile{{
				HostFilePath:      configPath,
				ContainerFilePath: "/etc/garage.toml",
				FileMode:          garageConfigFileMode,
			}},
			Cmd: []string{"/garage", "server", "--single-node", "--default-bucket"},
			WaitingFor: wait.ForListeningPort(garageS3Port).
				WithStartupTimeout(garageStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return s3.Config{}, container, tempDir, fmt.Errorf("start garage container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return s3.Config{}, container, tempDir, fmt.Errorf("read garage host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, garageS3Port)
	if err != nil {
		return s3.Config{}, container, tempDir, fmt.Errorf("read garage mapped port: %w", err)
	}

	config := s3.Config{
		Endpoint:        net.JoinHostPort(host, mappedPort.Port()),
		Bucket:          garageBucket,
		AccessKeyID:     garageAccessKeyID,
		SecretAccessKey: garageSecretAccessKey,
		Region:          garageRegion,
		PathStyle:       true,
	}
	if err := waitForGarageBucket(ctx, config); err != nil {
		return s3.Config{}, container, tempDir, err
	}

	return config, container, tempDir, nil
}

// writeGarageConfig renders the embedded garage.toml into a temporary file and
// returns the host path for the testcontainers file mount.
func writeGarageConfig() (string, string, error) {
	tempDir, err := os.MkdirTemp("", "imgsrv-garage-*")
	if err != nil {
		return "", "", fmt.Errorf("create garage config temp dir: %w", err)
	}

	path := filepath.Join(tempDir, "garage.toml")
	if err := os.WriteFile(path, []byte(garageConfig()), garageConfigFileMode); err != nil {
		return "", tempDir, fmt.Errorf("write garage config: %w", err)
	}

	return path, tempDir, nil
}

// garageConfig returns the static garage.toml the harness writes into the
// container, configured for single-node use with path-style S3 access.
func garageConfig() string {
	return `metadata_dir = "/tmp/garage/meta"
data_dir = "/tmp/garage/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "0000000000000000000000000000000000000000000000000000000000000000"
allow_world_readable_secrets = true

[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
root_domain = ".s3.garage.localhost"

[admin]
api_bind_addr = "[::]:3903"
admin_token = "test-admin-token"
metrics_token = "test-metrics-token"
`
}

// waitForGarageBucket polls the Garage S3 endpoint until the default bucket
// reports as existing or the configured ready timeout elapses.
func waitForGarageBucket(ctx context.Context, config s3.Config) error {
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(config.AccessKeyID, config.SecretAccessKey, ""),
		Secure:       config.UseTLS,
		Region:       config.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return fmt.Errorf("create garage readiness client: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, garageReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(garageReadyInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		exists, err := client.BucketExists(waitCtx, config.Bucket)
		if err == nil && exists {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("wait for garage bucket: %w", lastErr)
			}
			return fmt.Errorf("wait for garage bucket %q: %w", config.Bucket, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// openObjectStore constructs the S3-backed object store used by the in-process
// imgsrv server against the running Garage container.
func openObjectStore(t testing.TB, config s3.Config) objectstore.Store {
	t.Helper()

	store, err := newObjectStore(config)
	require.NoError(t, err)

	return store
}

func newObjectStore(config s3.Config) (objectstore.Store, error) {
	store, err := s3.New(config)
	if err != nil {
		return nil, err
	}

	return store, nil
}
