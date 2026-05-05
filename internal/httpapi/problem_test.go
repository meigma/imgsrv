package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProblemResponseMarshalsRFC9457MembersAndExtensions(t *testing.T) {
	body, err := json.Marshal(problemResponse{
		Type:     "about:blank",
		Title:    http.StatusText(http.StatusConflict),
		Status:   http.StatusConflict,
		Detail:   "upload conflict",
		Instance: "/problems/123",
		Extensions: map[string]json.RawMessage{
			"conflict_id": json.RawMessage(`"abc123"`),
			"status":      json.RawMessage(`200`),
		},
	})
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"type": "about:blank",
		"title": "Conflict",
		"status": 409,
		"detail": "upload conflict",
		"instance": "/problems/123",
		"conflict_id": "abc123"
	}`, string(body))
}
