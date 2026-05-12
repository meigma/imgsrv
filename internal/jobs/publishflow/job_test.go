package publishflow_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/imgsrv/internal/jobs/publishflow"
	"github.com/meigma/imgsrv/internal/jobs/publishflow/mocks"
	"github.com/meigma/imgsrv/internal/materialization/incus"
	"github.com/meigma/imgsrv/internal/publish"
)

func TestRunOnceReturnsIdleWhenNoStepIsClaimable(t *testing.T) {
	tc := newTestContext(t)
	tc.store.EXPECT().
		ClaimStep(mock.Anything, mock.MatchedBy(validClaimParams)).
		Return(publish.Step{}, fmt.Errorf("%w: no queued publish step", publish.ErrNotFound))

	got, err := tc.job.RunOnce(context.Background(), "worker-a")

	require.NoError(t, err)
	assert.False(t, got.Worked)
}

func TestRunOnceExecutesClaimedStep(t *testing.T) {
	tests := []struct {
		name  string
		step  publish.Step
		setup func(*testContext, publish.Step)
	}{
		{
			name: "validate catalog",
			step: stepFixture(publish.StepValidateCatalog),
			setup: func(tc *testContext, step publish.Step) {
				tc.store.EXPECT().
					SucceedValidateCatalogStep(mock.Anything, succeedStepParams(step)).
					Return(stepWithState(step, publish.StepStateSucceeded), nil)
			},
		},
		{
			name: "incus index",
			step: stepFixture(publish.StepIncusIndex),
			setup: func(tc *testContext, step publish.Step) {
				rows := []incus.ProjectionRow{{VersionID: step.VersionID, ArtifactID: uuid.New()}}
				tc.incus.EXPECT().
					IndexVersion(mock.Anything, incus.IndexVersionParams{
						VersionID: step.VersionID,
						ImageName: step.ImageName,
						Version:   step.Version,
					}).
					Return(rows, nil)
				tc.store.EXPECT().
					SucceedIncusIndexStep(mock.Anything, publish.SucceedIncusIndexStepParams{
						ID:           step.ID,
						WorkerID:     testWorkerID,
						AttemptCount: step.AttemptCount,
						VersionID:    step.VersionID,
						Rows:         rows,
					}).
					Return(stepWithState(step, publish.StepStateSucceeded), nil)
			},
		},
		{
			name: "finalize publish",
			step: stepFixture(publish.StepFinalizePublish),
			setup: func(tc *testContext, step publish.Step) {
				tc.store.EXPECT().
					FinalizePublishStep(mock.Anything, succeedStepParams(step)).
					Return(publish.Job{ID: step.JobID, State: publish.JobStateSucceeded}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tc.store.EXPECT().
				ClaimStep(mock.Anything, mock.MatchedBy(validClaimParams)).
				Return(tt.step, nil)
			tt.setup(tc, tt.step)

			got, err := tc.job.RunOnce(context.Background(), "worker-a")

			require.NoError(t, err)
			assert.True(t, got.Worked)
		})
	}
}

func TestRunOnceRecordsStepFailure(t *testing.T) {
	tc := newTestContext(t)
	step := stepFixture(publish.StepIncusIndex)
	wantErr := errors.New("metadata blob is missing")
	tc.store.EXPECT().
		ClaimStep(mock.Anything, mock.MatchedBy(validClaimParams)).
		Return(step, nil)
	tc.incus.EXPECT().
		IndexVersion(mock.Anything, mock.Anything).
		Return(nil, wantErr)
	tc.store.EXPECT().
		FailStep(mock.Anything, publish.FailStepParams{
			ID:             step.ID,
			WorkerID:       testWorkerID,
			AttemptCount:   step.AttemptCount,
			FailureMessage: wantErr.Error(),
		}).
		Return(stepWithState(step, publish.StepStateFailed), nil)

	got, err := tc.job.RunOnce(context.Background(), "worker-a")

	require.NoError(t, err)
	assert.True(t, got.Worked)
}

func TestRunOnceDoesNotFailReclaimedStep(t *testing.T) {
	t.Run("completion lease lost", func(t *testing.T) {
		tc := newTestContext(t)
		step := stepFixture(publish.StepValidateCatalog)
		tc.store.EXPECT().
			ClaimStep(mock.Anything, mock.MatchedBy(validClaimParams)).
			Return(step, nil)
		tc.store.EXPECT().
			SucceedValidateCatalogStep(mock.Anything, succeedStepParams(step)).
			Return(publish.Step{}, fmt.Errorf("%w: reclaimed", publish.ErrLeaseLost))

		got, err := tc.job.RunOnce(context.Background(), testWorkerID)

		require.NoError(t, err)
		assert.True(t, got.Worked)
	})

	t.Run("failure lease lost", func(t *testing.T) {
		tc := newTestContext(t)
		step := stepFixture(publish.StepIncusIndex)
		wantErr := errors.New("metadata blob is missing")
		tc.store.EXPECT().
			ClaimStep(mock.Anything, mock.MatchedBy(validClaimParams)).
			Return(step, nil)
		tc.incus.EXPECT().
			IndexVersion(mock.Anything, mock.Anything).
			Return(nil, wantErr)
		tc.store.EXPECT().
			FailStep(mock.Anything, publish.FailStepParams{
				ID:             step.ID,
				WorkerID:       testWorkerID,
				AttemptCount:   step.AttemptCount,
				FailureMessage: wantErr.Error(),
			}).
			Return(publish.Step{}, fmt.Errorf("%w: reclaimed", publish.ErrLeaseLost))

		got, err := tc.job.RunOnce(context.Background(), testWorkerID)

		require.NoError(t, err)
		assert.True(t, got.Worked)
	})
}

const testWorkerID = "worker-a"

type testContext struct {
	store *mocks.MockStepStore
	incus *mocks.MockIncusIndexer
	job   *publishflow.Job
}

func newTestContext(t *testing.T) *testContext {
	t.Helper()

	store := mocks.NewMockStepStore(t)
	incusIndexer := mocks.NewMockIncusIndexer(t)

	return &testContext{
		store: store,
		incus: incusIndexer,
		job: publishflow.New(publishflow.Config{
			Store:            store,
			Incus:            incusIndexer,
			StaleStepTimeout: time.Minute,
		}),
	}
}

func validClaimParams(params publish.ClaimStepParams) bool {
	return params.WorkerID == testWorkerID && !params.StaleRunningBefore.IsZero()
}

func stepFixture(name string) publish.Step {
	return publish.Step{
		ID:           uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		JobID:        uuid.MustParse("22222222-3333-4444-5555-666666666666"),
		VersionID:    uuid.MustParse("33333333-4444-5555-6666-777777777777"),
		ImageName:    "debian",
		Version:      "v1.0.0",
		Name:         name,
		State:        publish.StepStateRunning,
		Blocking:     true,
		Sequence:     10,
		AttemptCount: 1,
	}
}

func succeedStepParams(step publish.Step) publish.SucceedStepParams {
	return publish.SucceedStepParams{
		ID:           step.ID,
		WorkerID:     testWorkerID,
		AttemptCount: step.AttemptCount,
	}
}

func stepWithState(step publish.Step, state publish.StepState) publish.Step {
	step.State = state
	return step
}
