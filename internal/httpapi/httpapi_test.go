package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
