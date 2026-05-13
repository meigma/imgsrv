package logging

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactAttrRedactsSensitiveKeys(t *testing.T) {
	tests := []struct {
		key      string
		wantSafe bool
	}{
		{key: "authorization"},
		{key: "Authorization"},
		{key: "bearer_token"},
		{key: "api_token"},
		{key: "bootstrap-token"},
		{key: "password"},
		{key: "postgres_url"},
		{key: "secret_access_key"},
		{key: "session_token"},
		{key: "private_key"},
		{key: "token_id", wantSafe: true},
		{key: "principal_id", wantSafe: true},
		{key: "request_id", wantSafe: true},
		{key: "access_key_id"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := RedactAttr(nil, slog.String(tt.key, "secret-value"))
			if tt.wantSafe {
				assert.Equal(t, "secret-value", got.Value.String())
				return
			}

			assert.Equal(t, redactedValue, got.Value.String())
		})
	}
}

func TestRedactingHandlerOptionsRedactsEmittedLogs(t *testing.T) {
	logs := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(logs, RedactingHandlerOptions(slog.LevelDebug)))

	logger.Info("issued token", "token_id", "tok_123", "api_token", "secret-token")

	got := logs.String()
	require.NotEmpty(t, got)
	assert.Contains(t, got, `"token_id":"tok_123"`)
	assert.Contains(t, got, `"api_token":"[REDACTED]"`)
	assert.NotContains(t, got, "secret-token")
}

func TestSubjectHashIsStableAndNonRaw(t *testing.T) {
	got := SubjectHash("issuer", "raw-subject")

	assert.Len(t, got, 64)
	assert.Equal(t, got, SubjectHash("issuer", "raw-subject"))
	assert.NotContains(t, got, "raw-subject")
}
