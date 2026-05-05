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
	garageImage           = "dxflrs/garage:v2.3.0"
	garageS3Port          = "3900/tcp"
	garageRegion          = "garage"
	garageBucket          = "imgsrv-integration"
	garageAccessKeyID     = "GK00000000000000000000000000000000"
	garageSecretAccessKey = "0000000000000000000000000000000000000000000000000000000000000000"
	garageStartupTimeout  = time.Minute
	garageReadyTimeout    = 30 * time.Second
	garageReadyInterval   = 100 * time.Millisecond
	garageConfigFileMode  = 0600
)

func startGarage(ctx context.Context, t testing.TB) s3.Config {
	t.Helper()

	configPath := writeGarageConfig(t)
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
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, garageS3Port)
	require.NoError(t, err)

	config := s3.Config{
		Endpoint:        net.JoinHostPort(host, mappedPort.Port()),
		Bucket:          garageBucket,
		AccessKeyID:     garageAccessKeyID,
		SecretAccessKey: garageSecretAccessKey,
		Region:          garageRegion,
		PathStyle:       true,
	}
	waitForGarageBucket(ctx, t, config)

	return config
}

func writeGarageConfig(t testing.TB) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "garage.toml")
	err := os.WriteFile(path, []byte(garageConfig()), garageConfigFileMode)
	require.NoError(t, err)

	return path
}

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

func waitForGarageBucket(ctx context.Context, t testing.TB, config s3.Config) {
	t.Helper()

	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(config.AccessKeyID, config.SecretAccessKey, ""),
		Secure:       config.UseTLS,
		Region:       config.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, garageReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(garageReadyInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		exists, err := client.BucketExists(waitCtx, config.Bucket)
		if err == nil && exists {
			return
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				require.NoError(t, fmt.Errorf("wait for garage bucket: %w", lastErr))
			}
			require.NoError(t, fmt.Errorf("wait for garage bucket %q: %w", config.Bucket, waitCtx.Err()))
		case <-ticker.C:
		}
	}
}

func openObjectStore(t testing.TB, config s3.Config) objectstore.Store {
	t.Helper()

	store, err := s3.New(config)
	require.NoError(t, err)

	return store
}
