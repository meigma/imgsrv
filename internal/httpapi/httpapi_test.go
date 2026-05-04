package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationalEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		readiness ReadinessChecker
		wantCode  int
		wantBody  string
	}{
		{
			name:     "health reports process liveness",
			method:   http.MethodGet,
			path:     "/healthz",
			wantCode: http.StatusNoContent,
		},
		{
			name:     "health supports HEAD through GET route",
			method:   http.MethodHead,
			path:     "/healthz",
			wantCode: http.StatusNoContent,
		},
		{
			name:     "readiness succeeds when checker succeeds",
			method:   http.MethodGet,
			path:     "/readyz",
			wantCode: http.StatusNoContent,
		},
		{
			name:   "readiness supports HEAD through GET route",
			method: http.MethodHead,
			path:   "/readyz",
			readiness: ReadinessFunc(func(context.Context) error {
				return nil
			}),
			wantCode: http.StatusNoContent,
		},
		{
			name:   "readiness fails when checker fails",
			method: http.MethodGet,
			path:   "/readyz",
			readiness: ReadinessFunc(func(context.Context) error {
				return errors.New("not ready")
			}),
			wantCode: http.StatusServiceUnavailable,
			wantBody: http.StatusText(http.StatusServiceUnavailable) + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := New(Dependencies{Readiness: tt.readiness})
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantCode, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestNewLogsRequestsWithRoute(t *testing.T) {
	logs := new(strings.Builder)
	logger := slog.New(slog.NewTextHandler(logs, nil))
	handler := New(Dependencies{Logger: logger})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("User-Agent", "imgsrv-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	got := logs.String()
	assert.Contains(t, got, "level=INFO")
	assert.Contains(t, got, `msg="http request"`)
	assert.Contains(t, got, "method=GET")
	assert.Contains(t, got, "path=/healthz")
	assert.Contains(t, got, "status=204")
	assert.Contains(t, got, "bytes=0")
	assert.Contains(t, got, "remote_addr=192.0.2.1:1234")
	assert.Contains(t, got, "user_agent=imgsrv-test")
	assert.Contains(t, got, `route="GET /healthz"`)
}

func TestLogRequestsUsesStatusLevel(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{
			name:      "logs success at info",
			status:    http.StatusNoContent,
			wantLevel: "level=INFO",
		},
		{
			name:      "logs client error at warn",
			status:    http.StatusNotFound,
			wantLevel: "level=WARN",
		},
		{
			name:      "logs server error at error",
			status:    http.StatusInternalServerError,
			wantLevel: "level=ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := new(strings.Builder)
			logger := slog.New(slog.NewTextHandler(logs, nil))
			handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}), logRequests(logger))
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, tt.status, rec.Code)
			assert.Contains(t, logs.String(), tt.wantLevel)
			assert.Contains(t, logs.String(), fmt.Sprintf("status=%d", tt.status))
		})
	}
}
