package jobs

import "fmt"

// Identity identifies one process run for durable background job state.
type Identity struct {
	// NodeName identifies the process node running background jobs.
	NodeName string

	// RunID identifies one run of the process on the node.
	RunID string
}

// WorkerID returns the durable worker ID for a named background job.
func (identity Identity) WorkerID(jobName string) string {
	return fmt.Sprintf("%s/%s/%s", identity.NodeName, identity.RunID, jobName)
}
