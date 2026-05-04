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
