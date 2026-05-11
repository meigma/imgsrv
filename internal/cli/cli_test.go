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
				Listen:                             ":8080",
				LogFormat:                          "text",
				Verbosity:                          "info",
				MetricsListen:                      "127.0.0.1:9464",
				MetricsPath:                        "/metrics",
				UploadTTL:                          24 * time.Hour,
				CASPromotionPollInterval:           5 * time.Second,
				CASPromotionErrorBackoffInitial:    5 * time.Second,
				CASPromotionErrorBackoffMax:        time.Minute,
				CASPromotionCircuitBreakerFailures: 10,
				CASPromotionCircuitBreakerCooldown: time.Minute,
				ShutdownTimeout:                    10 * time.Second,
			},
		},
		{
			name: "uses environment variables",
			env: map[string]string{
				"IMGSRV_LISTEN":                                 "127.0.0.1:9090",
				"IMGSRV_NODE_NAME":                              "env-node",
				"IMGSRV_LOG_FORMAT":                             "json",
				"IMGSRV_VERBOSITY":                              "debug",
				"IMGSRV_METRICS_LISTEN":                         "127.0.0.1:9091",
				"IMGSRV_METRICS_PATH":                           "/internal/metrics",
				"IMGSRV_POSTGRES_URL":                           "postgres://env",
				"IMGSRV_S3_ENDPOINT":                            "garage.env:3900",
				"IMGSRV_S3_BUCKET":                              "imgsrv-env",
				"IMGSRV_S3_ACCESS_KEY_ID":                       "env-access",
				"IMGSRV_S3_SECRET_ACCESS_KEY":                   "env-secret",
				"IMGSRV_S3_SESSION_TOKEN":                       "env-session",
				"IMGSRV_S3_REGION":                              "garage",
				"IMGSRV_S3_USE_TLS":                             "false",
				"IMGSRV_S3_PATH_STYLE":                          "true",
				"IMGSRV_UPLOAD_TTL":                             "2h",
				"IMGSRV_CAS_PROMOTION_ENABLED":                  "true",
				"IMGSRV_CAS_PROMOTION_POLL_INTERVAL":            "11s",
				"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF":            "2s",
				"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX":        "30s",
				"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES": "4",
				"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN": "45s",
				"IMGSRV_SHUTDOWN_TIMEOUT":                       "3s",
			},
			want: app.Config{
				Listen:                             "127.0.0.1:9090",
				NodeName:                           "env-node",
				LogFormat:                          "json",
				Verbosity:                          "debug",
				MetricsListen:                      "127.0.0.1:9091",
				MetricsPath:                        "/internal/metrics",
				PostgresURL:                        "postgres://env",
				S3Endpoint:                         "garage.env:3900",
				S3Bucket:                           "imgsrv-env",
				S3AccessKeyID:                      "env-access",
				S3SecretAccessKey:                  "env-secret",
				S3SessionToken:                     "env-session",
				S3Region:                           "garage",
				S3UseTLS:                           false,
				S3PathStyle:                        true,
				UploadTTL:                          2 * time.Hour,
				CASPromotionEnabled:                true,
				CASPromotionPollInterval:           11 * time.Second,
				CASPromotionErrorBackoffInitial:    2 * time.Second,
				CASPromotionErrorBackoffMax:        30 * time.Second,
				CASPromotionCircuitBreakerFailures: 4,
				CASPromotionCircuitBreakerCooldown: 45 * time.Second,
				ShutdownTimeout:                    3 * time.Second,
			},
		},
		{
			name: "flags override environment variables",
			env: map[string]string{
				"IMGSRV_LISTEN":                                 "127.0.0.1:9090",
				"IMGSRV_NODE_NAME":                              "env-node",
				"IMGSRV_LOG_FORMAT":                             "json",
				"IMGSRV_VERBOSITY":                              "debug",
				"IMGSRV_METRICS_LISTEN":                         "127.0.0.1:9091",
				"IMGSRV_METRICS_PATH":                           "/internal/metrics",
				"IMGSRV_POSTGRES_URL":                           "postgres://env",
				"IMGSRV_S3_ENDPOINT":                            "garage.env:3900",
				"IMGSRV_S3_BUCKET":                              "imgsrv-env",
				"IMGSRV_S3_ACCESS_KEY_ID":                       "env-access",
				"IMGSRV_S3_SECRET_ACCESS_KEY":                   "env-secret",
				"IMGSRV_S3_SESSION_TOKEN":                       "env-session",
				"IMGSRV_S3_REGION":                              "garage",
				"IMGSRV_S3_USE_TLS":                             "true",
				"IMGSRV_S3_PATH_STYLE":                          "false",
				"IMGSRV_UPLOAD_TTL":                             "2h",
				"IMGSRV_CAS_PROMOTION_ENABLED":                  "false",
				"IMGSRV_CAS_PROMOTION_POLL_INTERVAL":            "11s",
				"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF":            "2s",
				"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX":        "30s",
				"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES": "4",
				"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN": "45s",
				"IMGSRV_SHUTDOWN_TIMEOUT":                       "3s",
			},
			args: []string{
				"--listen=127.0.0.1:7070",
				"--node-name=flag-node",
				"--log-format=text",
				"--verbosity=warn",
				"--metrics-listen=",
				"--metrics-path=/scrape",
				"--postgres-url=postgres://flag",
				"--s3-endpoint=garage.flag:3900",
				"--s3-bucket=imgsrv-flag",
				"--s3-access-key-id=flag-access",
				"--s3-secret-access-key=flag-secret",
				"--s3-session-token=flag-session",
				"--s3-region=garage-flag",
				"--s3-use-tls=false",
				"--s3-path-style=true",
				"--upload-ttl=4h",
				"--cas-promotion-enabled",
				"--cas-promotion-poll-interval=13s",
				"--cas-promotion-error-backoff=3s",
				"--cas-promotion-error-backoff-max=35s",
				"--cas-promotion-circuit-breaker-failures=5",
				"--cas-promotion-circuit-breaker-cooldown=55s",
				"--shutdown-timeout=5s",
			},
			want: app.Config{
				Listen:                             "127.0.0.1:7070",
				NodeName:                           "flag-node",
				LogFormat:                          "text",
				Verbosity:                          "warn",
				MetricsListen:                      "",
				MetricsPath:                        "/scrape",
				PostgresURL:                        "postgres://flag",
				S3Endpoint:                         "garage.flag:3900",
				S3Bucket:                           "imgsrv-flag",
				S3AccessKeyID:                      "flag-access",
				S3SecretAccessKey:                  "flag-secret",
				S3SessionToken:                     "flag-session",
				S3Region:                           "garage-flag",
				S3UseTLS:                           false,
				S3PathStyle:                        true,
				UploadTTL:                          4 * time.Hour,
				CASPromotionEnabled:                true,
				CASPromotionPollInterval:           13 * time.Second,
				CASPromotionErrorBackoffInitial:    3 * time.Second,
				CASPromotionErrorBackoffMax:        35 * time.Second,
				CASPromotionCircuitBreakerFailures: 5,
				CASPromotionCircuitBreakerCooldown: 55 * time.Second,
				ShutdownTimeout:                    5 * time.Second,
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
	assert.Contains(t, stdout.String(), "--node-name")
	assert.Contains(t, stdout.String(), "--log-format")
	assert.Contains(t, stdout.String(), "--verbosity")
	assert.Contains(t, stdout.String(), "--metrics-listen")
	assert.Contains(t, stdout.String(), "--metrics-path")
	assert.Contains(t, stdout.String(), "--postgres-url")
	assert.NotContains(t, stdout.String(), "--oidc-issuer-url")
	assert.NotContains(t, stdout.String(), "--github-oidc-audience")
	assert.Contains(t, stdout.String(), "--s3-endpoint")
	assert.Contains(t, stdout.String(), "--upload-ttl")
	assert.Contains(t, stdout.String(), "--cas-promotion-enabled")
	assert.Contains(t, stdout.String(), "--cas-promotion-poll-interval")
	assert.Contains(t, stdout.String(), "--cas-promotion-error-backoff")
	assert.Contains(t, stdout.String(), "--cas-promotion-error-backoff-max")
	assert.Contains(t, stdout.String(), "--cas-promotion-circuit-breaker-failures")
	assert.Contains(t, stdout.String(), "--cas-promotion-circuit-breaker-cooldown")
	assert.NotContains(t, stdout.String(), "--cas-promotion-worker-id")
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
		"IMGSRV_NODE_NAME",
		"IMGSRV_LOG_FORMAT",
		"IMGSRV_LOG_LEVEL",
		"IMGSRV_VERBOSITY",
		"IMGSRV_METRICS_LISTEN",
		"IMGSRV_METRICS_PATH",
		"IMGSRV_POSTGRES_URL",
		"IMGSRV_S3_ENDPOINT",
		"IMGSRV_S3_BUCKET",
		"IMGSRV_S3_ACCESS_KEY_ID",
		"IMGSRV_S3_SECRET_ACCESS_KEY",
		"IMGSRV_S3_SESSION_TOKEN",
		"IMGSRV_S3_REGION",
		"IMGSRV_S3_USE_TLS",
		"IMGSRV_S3_PATH_STYLE",
		"IMGSRV_UPLOAD_TTL",
		"IMGSRV_CAS_PROMOTION_ENABLED",
		"IMGSRV_CAS_PROMOTION_POLL_INTERVAL",
		"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF",
		"IMGSRV_CAS_PROMOTION_ERROR_BACKOFF_MAX",
		"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_FAILURES",
		"IMGSRV_CAS_PROMOTION_CIRCUIT_BREAKER_COOLDOWN",
		"IMGSRV_SHUTDOWN_TIMEOUT",
	} {
		oldValue, hadValue := os.LookupEnv(key)
		if hadValue {
			t.Setenv(key, oldValue)
		}
		require.NoError(t, os.Unsetenv(key))
	}
}
