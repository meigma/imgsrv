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

func TestNewLoggerValidatesFormatAndLevel(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		level   string
		wantErr string
	}{
		{
			name:   "accepts text info",
			format: "text",
			level:  "info",
		},
		{
			name:   "accepts json debug",
			format: "json",
			level:  "debug",
		},
		{
			name:    "rejects unknown format",
			format:  "console",
			level:   "info",
			wantErr: "unsupported log format",
		},
		{
			name:    "rejects unknown level",
			format:  "text",
			level:   "trace",
			wantErr: "unsupported log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := NewLogger(io.Discard, tt.format, tt.level)

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
