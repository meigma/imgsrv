package jobs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/imgsrv/internal/jobs"
)

func TestIdentityWorkerIDUsesJobSuffix(t *testing.T) {
	identity := jobs.Identity{
		NodeName: "node-a",
		RunID:    "run-b",
	}

	got := identity.WorkerID("cas-promotion")

	assert.Equal(t, "node-a/run-b/cas-promotion", got)
}
