package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExposesPrometheusMetricsHandler(t *testing.T) {
	providers, err := New(Config{
		ServiceName:    "imgsrv-test",
		ServiceVersion: "v1.2.3",
		MetricsPath:    "/metrics",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, providers.Shutdown(context.Background()))
	})

	counter, err := providers.Meter("github.com/meigma/imgsrv/internal/telemetry_test").
		Int64Counter("imgsrv_test_events")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	providers.MetricsHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "imgsrv_test_events")
	assert.Contains(t, rec.Body.String(), `service_name="imgsrv-test"`)
	assert.Contains(t, rec.Body.String(), `service_version="v1.2.3"`)
}

func TestNewRejectsMetricsPathWithoutLeadingSlash(t *testing.T) {
	providers, err := New(Config{MetricsPath: "metrics"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with /")
	assert.Nil(t, providers)
}

func TestWrapHTTPHandlerInstrumentsRequestMetrics(t *testing.T) {
	providers, err := New(Config{MetricsPath: "/metrics"})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, providers.Shutdown(context.Background()))
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := providers.WrapHTTPHandler(mux)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ready", nil))

	rec := httptest.NewRecorder()
	providers.MetricsHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.True(t,
		strings.Contains(body, "http_server") || strings.Contains(body, "http.server"),
		"expected HTTP server metrics, got:\n%s", body,
	)
}
