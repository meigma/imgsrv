package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerServeStartsAndShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server, err := NewServer(Config{
		Listen:          listener.Addr().String(),
		ShutdownTimeout: time.Second,
	}, Dependencies{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx, listener)
	}()

	assert.Eventually(t, func() bool {
		resp, err := http.Get("http://" + listener.Addr().String() + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	}, time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerServeStartsMetricsServer(t *testing.T) {
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	metricsListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server, err := NewServer(Config{
		Listen:          apiListener.Addr().String(),
		MetricsListen:   metricsListener.Addr().String(),
		MetricsPath:     "/metrics",
		ShutdownTimeout: time.Second,
	}, Dependencies{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx, apiListener, metricsListener)
	}()

	assert.Eventually(t, func() bool {
		resp, err := http.Get("http://" + apiListener.Addr().String() + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	}, time.Second, 10*time.Millisecond)

	assert.Eventually(t, func() bool {
		resp, err := http.Get("http://" + metricsListener.Addr().String() + "/metrics")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerServeRunsBackgroundJobs(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	started := make(chan struct{})
	stopped := make(chan struct{})

	server, err := NewServer(Config{
		Listen:          listener.Addr().String(),
		ShutdownTimeout: time.Second,
	}, Dependencies{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackgroundJobs: []BackgroundJob{backgroundJobFunc(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil
		})},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx, listener)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background job did not start")
	}

	cancel()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("background job did not stop")
	}
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestNewLoggerValidatesFormatAndVerbosity(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		verbosity string
		wantErr   string
	}{
		{
			name:      "accepts text info",
			format:    "text",
			verbosity: "info",
		},
		{
			name:      "accepts json debug",
			format:    "json",
			verbosity: "debug",
		},
		{
			name:      "rejects unknown format",
			format:    "console",
			verbosity: "info",
			wantErr:   "unsupported log format",
		},
		{
			name:      "rejects unknown verbosity",
			format:    "text",
			verbosity: "trace",
			wantErr:   "unsupported verbosity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := NewLogger(io.Discard, tt.format, tt.verbosity)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, logger)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, logger)
		})
	}
}

func TestNewServerRejectsInvalidMetricsPath(t *testing.T) {
	server, err := NewServer(Config{
		MetricsListen: "127.0.0.1:9464",
		MetricsPath:   "metrics",
	}, Dependencies{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with /")
	assert.Nil(t, server)
}

func TestConfigWithDefaultsSetsBackgroundDefaults(t *testing.T) {
	got := (Config{}).withDefaults()

	assert.Equal(t, 24*time.Hour, got.UploadTTL)
	assert.NotEmpty(t, got.NodeName)
	assert.Len(t, got.RunID, defaultRunIDLength)
	assert.Regexp(t, "^[0-9a-f]{10}$", got.RunID)
	assert.Equal(t, 5*time.Second, got.CASPromotionPollInterval)
	assert.Equal(t, 5*time.Second, got.CASPromotionErrorBackoffInitial)
	assert.Equal(t, time.Minute, got.CASPromotionErrorBackoffMax)
	assert.Equal(t, 10, got.CASPromotionCircuitBreakerFailures)
	assert.Equal(t, time.Minute, got.CASPromotionCircuitBreakerCooldown)
}

func TestConfigWithDefaultsPreservesProcessIdentity(t *testing.T) {
	got := (Config{
		NodeName: "node-a",
		RunID:    "run-b",
	}).withDefaults()

	assert.Equal(t, "node-a", got.NodeName)
	assert.Equal(t, "run-b", got.RunID)
}

func TestNewAuthServiceValidatesOIDCConfigPair(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "issuer only",
			cfg: Config{
				OIDCIssuerURL: "https://issuer.example",
			},
			want: "must be set together",
		},
		{
			name: "audience only",
			cfg: Config{
				OIDCAudience: "imgsrv-api",
			},
			want: "must be set together",
		},
		{
			name: "scope only",
			cfg: Config{
				OIDCRequiredScope: "imgsrv.write",
			},
			want: "must be set together",
		},
		{
			name: "issuer and audience only",
			cfg: Config{
				OIDCIssuerURL: "https://issuer.example",
				OIDCAudience:  "imgsrv-api",
			},
			want: "must be set together",
		},
		{
			name: "relative issuer",
			cfg: Config{
				OIDCIssuerURL:     "issuer.example",
				OIDCAudience:      "imgsrv-api",
				OIDCRequiredScope: "imgsrv.write",
			},
			want: "must be an absolute URL",
		},
		{
			name: "http issuer",
			cfg: Config{
				OIDCIssuerURL:     "http://issuer.example",
				OIDCAudience:      "imgsrv-api",
				OIDCRequiredScope: "imgsrv.write",
			},
			want: "must use https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAuthService(context.Background(), tt.cfg, nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Nil(t, got.service)
		})
	}
}

func TestNewAuthServiceValidatesGitHubOIDCConfigPair(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "audience only",
			cfg: Config{
				GitHubOIDCAudience: "imgsrv-github",
			},
		},
		{
			name: "repository id only",
			cfg: Config{
				GitHubOIDCRepositoryID: "123456789",
			},
		},
		{
			name: "workflow ref only",
			cfg: Config{
				GitHubOIDCWorkflowRef: "meigma/imgsrv/.github/workflows/publish.yml@refs/heads/main",
			},
		},
		{
			name: "audience and repository id only",
			cfg: Config{
				GitHubOIDCAudience:     "imgsrv-github",
				GitHubOIDCRepositoryID: "123456789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAuthService(context.Background(), tt.cfg, nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be set together")
			assert.Nil(t, got.service)
		})
	}
}

func TestNewUploadServiceRequiresPostgresWhenS3Configured(t *testing.T) {
	got, err := newUploadService(Config{
		S3Endpoint:        "garage.local:3900",
		S3Bucket:          "imgsrv",
		S3AccessKeyID:     "access",
		S3SecretAccessKey: "secret",
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres url")
	assert.Nil(t, got.service)
}

func TestNewCASPromotionJobsRequiresDependenciesWhenEnabled(t *testing.T) {
	got, err := newCASPromotionJobs(Config{
		CASPromotionEnabled:                true,
		NodeName:                           "node-a",
		RunID:                              "run-b",
		CASPromotionPollInterval:           time.Second,
		CASPromotionErrorBackoffInitial:    time.Second,
		CASPromotionErrorBackoffMax:        time.Minute,
		CASPromotionCircuitBreakerFailures: 10,
		CASPromotionCircuitBreakerCooldown: time.Minute,
	}, nil, nil, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres url")
	assert.Nil(t, got)
}

type backgroundJobFunc func(context.Context) error

func (f backgroundJobFunc) Run(ctx context.Context) error {
	return f(ctx)
}
