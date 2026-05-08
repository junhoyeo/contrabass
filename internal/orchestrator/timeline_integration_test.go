package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type linearObservingTracker struct{ *observingTracker }

func (t *linearObservingTracker) IsLinearTracker() bool { return true }

func timelineNode(snapshot timeline.WorkflowTimelineSnapshot, nodeID string) (timeline.WorkflowNodeSummary, bool) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return timeline.WorkflowNodeSummary{}, false
}

func TestOrchestratorRecordsClaimFailureTimelineNode(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Identifier: "ENG-1", Title: "Test", State: types.Unclaimed}})
	mt.base.ClaimErr = errors.New("claim boom")
	store := timeline.NewStore(t.TempDir())
	orch := NewOrchestrator(mt, workspace.NewMockManager(t.TempDir()), &agent.MockRunner{}, &staticConfig{cfg: testConfig()}, nil)
	orch.SetWorkflowTimeline(store, false)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		snapshot, err := store.Snapshot(context.Background(), "ISS-1")
		if err != nil {
			return false
		}
		_, ok := timelineNode(snapshot, timeline.NodeID(1, "claim-failed"))
		return ok
	}, time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
	snapshot, err := store.Snapshot(context.Background(), "ISS-1")
	require.NoError(t, err)
	node, ok := timelineNode(snapshot, timeline.NodeID(1, "claim-failed"))
	require.True(t, ok)
	assert.Equal(t, timeline.NodeStatusFailed, node.Status)
	assert.True(t, node.Syncable)
	assert.Contains(t, node.Error, "claim boom")
}

func TestOrchestratorRecordsAgentStartFailureAndRetryTimelineNodes(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Identifier: "ENG-1", Title: "Test", State: types.Unclaimed}})
	store := timeline.NewStore(t.TempDir())
	cfg := testConfig()
	cfg.MaxRetryBackoffMsRaw = 1000
	orch := NewOrchestrator(mt, workspace.NewMockManager(t.TempDir()), &agent.MockRunner{StartErr: errors.New("spawn boom")}, &staticConfig{cfg: cfg}, nil)
	orch.SetWorkflowTimeline(store, false)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		snapshot, err := store.Snapshot(context.Background(), "ISS-1")
		if err != nil {
			return false
		}
		_, startFailed := timelineNode(snapshot, timeline.NodeID(1, "agent-start-failed"))
		_, retryQueued := timelineNode(snapshot, timeline.NodeID(1, "retry-queued"))
		return startFailed && retryQueued
	}, time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestratorSuppressesLegacyLinearCompletionCommentsWhenSyncEnabled(t *testing.T) {
	base := newObservingTracker([]types.Issue{{ID: "ISS-1", Identifier: "ENG-1", Title: "Test", State: types.Unclaimed}})
	mt := &linearObservingTracker{observingTracker: base}
	store := timeline.NewStore(t.TempDir())
	orch := NewOrchestrator(mt, workspace.NewMockManager(t.TempDir()), &agent.MockRunner{Events: []types.AgentEvent{{Type: "turn/completed"}}}, &staticConfig{cfg: testConfig()}, nil)
	orch.SetWorkflowTimeline(store, true)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool { return mt.ReleaseCount("ISS-1") > 0 }, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	assert.True(t, tracker.IsLinearTracker(mt))
	assert.Empty(t, mt.base.Comments["ISS-1"])
	snapshot, err := store.Snapshot(context.Background(), "ISS-1")
	require.NoError(t, err)
	_, ok := timelineNode(snapshot, timeline.NodeID(1, "complete"))
	assert.True(t, ok)
}

func TestOrchestratorPreservesLegacyCommentsWhenSyncDisabled(t *testing.T) {
	base := newObservingTracker([]types.Issue{{ID: "ISS-1", Identifier: "ENG-1", Title: "Test", State: types.Unclaimed}})
	mt := &linearObservingTracker{observingTracker: base}
	orch := NewOrchestrator(mt, workspace.NewMockManager(t.TempDir()), &agent.MockRunner{Events: []types.AgentEvent{{Type: "turn/completed"}}}, &staticConfig{cfg: testConfig()}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool { return mt.ReleaseCount("ISS-1") > 0 }, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.NotEmpty(t, mt.base.Comments["ISS-1"])
	assert.Contains(t, mt.base.Comments["ISS-1"][0], "Agent run completed")
}
