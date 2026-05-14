//go:build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/integration/harness"
)

func TestMetricsEndpointExposesApplicationMetrics(t *testing.T) {
	env := startEnv(t, harness.WithAPIToken(), harness.WithMetrics())
	ctx := t.Context()
	client := newClient(t, env)

	uploadBlobToCAS(ctx, t, env, client, []byte("imgsrv metrics integration payload"))

	body := eventuallyScrapeMetrics(t, env.MetricsURL()+"/metrics")

	assert.Contains(t, body, "imgsrv_postgres_pool_connections")
	assert.Contains(t, body, "imgsrv_upload_sessions")
	assert.Contains(t, body, "imgsrv_cas_ingest_jobs")
	assert.Contains(t, body, "imgsrv_cas_blobs")
	assert.Contains(t, body, "imgsrv_objectstore_operations_total")
	assert.Contains(t, body, "imgsrv_objectstore_bytes_total")
	assert.Contains(t, body, "imgsrv_background_job_attempts_total")
	assert.Contains(t, body, `job="publish"`)
}

func eventuallyScrapeMetrics(t *testing.T, metricsURL string) string {
	t.Helper()
	require.NotEmpty(t, metricsURL)

	var body string
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, metricsURL, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			return false
		}
		body = string(raw)
		return strings.Contains(body, "imgsrv_background_job_attempts_total")
	}, 5*time.Second, 25*time.Millisecond)

	return body
}
