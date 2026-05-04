package cli

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/app"
)

func TestExecuteContextResolvesConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want app.Config
	}{
		{
			name: "uses defaults",
			want: app.Config{
				Listen:          ":8080",
				LogFormat:       "text",
				Verbosity:       "info",
				MetricsListen:   "127.0.0.1:9464",
				MetricsPath:     "/metrics",
				ShutdownTimeout: 10 * time.Second,
			},
		},
		{
			name: "uses environment variables",
			env: map[string]string{
				"IMGSRV_LISTEN":           "127.0.0.1:9090",
				"IMGSRV_LOG_FORMAT":       "json",
				"IMGSRV_VERBOSITY":        "debug",
				"IMGSRV_METRICS_LISTEN":   "127.0.0.1:9091",
				"IMGSRV_METRICS_PATH":     "/internal/metrics",
				"IMGSRV_POSTGRES_URL":     "postgres://env",
				"IMGSRV_SHUTDOWN_TIMEOUT": "3s",
			},
			want: app.Config{
				Listen:          "127.0.0.1:9090",
				LogFormat:       "json",
				Verbosity:       "debug",
				MetricsListen:   "127.0.0.1:9091",
				MetricsPath:     "/internal/metrics",
				PostgresURL:     "postgres://env",
				ShutdownTimeout: 3 * time.Second,
			},
		},
		{
			name: "flags override environment variables",
			env: map[string]string{
				"IMGSRV_LISTEN":           "127.0.0.1:9090",
				"IMGSRV_LOG_FORMAT":       "json",
				"IMGSRV_VERBOSITY":        "debug",
				"IMGSRV_METRICS_LISTEN":   "127.0.0.1:9091",
				"IMGSRV_METRICS_PATH":     "/internal/metrics",
				"IMGSRV_POSTGRES_URL":     "postgres://env",
				"IMGSRV_SHUTDOWN_TIMEOUT": "3s",
			},
			args: []string{
				"--listen=127.0.0.1:7070",
				"--log-format=text",
				"--verbosity=warn",
				"--metrics-listen=",
				"--metrics-path=/scrape",
				"--postgres-url=postgres://flag",
				"--shutdown-timeout=5s",
			},
			want: app.Config{
				Listen:          "127.0.0.1:7070",
				LogFormat:       "text",
				Verbosity:       "warn",
				MetricsListen:   "",
				MetricsPath:     "/scrape",
				PostgresURL:     "postgres://flag",
				ShutdownTimeout: 5 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unsetConfigEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got, called, err := executeForConfig(t, tt.args)

			require.NoError(t, err)
			require.True(t, called, "expected imgsrv runner to be called")
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExecuteContextRejectsInvalidEnums(t *testing.T) {
	unsetConfigEnv(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rejects invalid log format",
			args: []string{"--log-format=console"},
			want: "log-format",
		},
		{
			name: "rejects invalid verbosity",
			args: []string{"--verbosity=trace"},
			want: "verbosity",
		},
		{
			name: "does not support removed log-level flag",
			args: []string{"--log-level=debug"},
			want: "log-level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, called, err := executeForConfig(t, tt.args)

			require.Error(t, err)
			assert.False(t, called, "runner should not start when CLI validation fails")
			assert.Zero(t, got)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestExecuteContextPrintsHelpWithoutStartingServer(t *testing.T) {
	unsetConfigEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false

	err := ExecuteContext(context.Background(), []string{"--help"}, func(context.Context, app.Config) error {
		called = true
		return nil
	}, &stdout, &stderr)

	require.NoError(t, err)
	assert.False(t, called, "help should not start the server")
	assert.Contains(t, stdout.String(), "Usage: imgsrv")
	assert.Contains(t, stdout.String(), "--listen")
	assert.Contains(t, stdout.String(), "--log-format")
	assert.Contains(t, stdout.String(), "--verbosity")
	assert.Contains(t, stdout.String(), "--metrics-listen")
	assert.Contains(t, stdout.String(), "--metrics-path")
	assert.Contains(t, stdout.String(), "--postgres-url")
	assert.NotContains(t, stdout.String(), "--log-level")
	assert.Empty(t, stderr.String())
}

func executeForConfig(t *testing.T, args []string) (app.Config, bool, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var got app.Config
	called := false

	err := ExecuteContext(context.Background(), args, func(_ context.Context, cfg app.Config) error {
		called = true
		got = cfg
		return nil
	}, &stdout, &stderr)

	return got, called, err
}

func unsetConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"IMGSRV_LISTEN",
		"IMGSRV_LOG_FORMAT",
		"IMGSRV_LOG_LEVEL",
		"IMGSRV_VERBOSITY",
		"IMGSRV_METRICS_LISTEN",
		"IMGSRV_METRICS_PATH",
		"IMGSRV_POSTGRES_URL",
		"IMGSRV_SHUTDOWN_TIMEOUT",
	} {
		oldValue, hadValue := os.LookupEnv(key)
		if hadValue {
			t.Setenv(key, oldValue)
		}
		require.NoError(t, os.Unsetenv(key))
	}
}
