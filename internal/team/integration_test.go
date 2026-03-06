package team

import (
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestStore creates a temporary Store for testing.
func setupTestStore(t *testing.T) (*Store, *Paths) {
	t.Helper()
	dir := t.TempDir()
	paths := NewPaths(dir)
	store := NewStore(paths)
	return store, paths
}

// setupTestPhases creates a Store, Paths, TaskRegistry, and PhaseMachine for testing.
func setupTestPhases(t *testing.T, maxFixLoops int) (*Store, *Paths, *TaskRegistry, *PhaseMachine) {
	t.Helper()
	store, paths := setupTestStore(t)
	tasks := NewTaskRegistry(store, paths, 300)
	phases := NewPhaseMachine(store, tasks, maxFixLoops)

	// Create initial manifest to ensure dirs exist
	_, err := store.CreateManifest("test-team", types.TeamConfig{MaxWorkers: 2, MaxFixLoops: maxFixLoops})
	require.NoError(t, err)

	return store, paths, tasks, phases
}

// TestStoreLifecycle tests creating, loading, updating, and deleting a team manifest.
func TestStoreLifecycle(t *testing.T) {
	store, _ := setupTestStore(t)

	// Create manifest
	cfg := types.TeamConfig{MaxWorkers: 3, MaxFixLoops: 2}
	manifest, err := store.CreateManifest("test-team", cfg)
	require.NoError(t, err)
	require.NotNil(t, manifest)
	assert.Equal(t, "test-team", manifest.Name)
	assert.Equal(t, 3, manifest.Config.MaxWorkers)
	assert.Equal(t, 2, manifest.Config.MaxFixLoops)

	// Load manifest
	loaded, err := store.LoadManifest("test-team")
	require.NoError(t, err)
	assert.Equal(t, manifest.Name, loaded.Name)
	assert.Equal(t, manifest.Config.MaxWorkers, loaded.Config.MaxWorkers)

	// Update manifest
	err = store.UpdateManifest("test-team", func(m *types.TeamManifest) error {
		m.Config.MaxWorkers = 5
		return nil
	})
	require.NoError(t, err)

	// Verify update
	updated, err := store.LoadManifest("test-team")
	require.NoError(t, err)
	assert.Equal(t, 5, updated.Config.MaxWorkers)

	// Verify team exists
	assert.True(t, store.TeamExists("test-team"))

	// Delete team
	err = store.DeleteTeam("test-team")
	require.NoError(t, err)

	// Verify team no longer exists
	assert.False(t, store.TeamExists("test-team"))
}

// TestPhaseTransitions tests valid phase transitions through the pipeline.
func TestPhaseTransitions(t *testing.T) {
	_, _, _, phases := setupTestPhases(t, 2)

	// Verify initial phase is PhasePlan
	phase, err := phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhasePlan, phase)

	// Transition: PhasePlan -> PhasePRD
	err = phases.Transition("test-team", types.PhasePRD, "requirements gathered")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhasePRD, phase)

	// Transition: PhasePRD -> PhaseExec
	err = phases.Transition("test-team", types.PhaseExec, "prd approved")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseExec, phase)

	// Transition: PhaseExec -> PhaseVerify
	err = phases.Transition("test-team", types.PhaseVerify, "execution complete")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseVerify, phase)

	// Transition: PhaseVerify -> PhaseComplete
	err = phases.Transition("test-team", types.PhaseComplete, "all verified")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseComplete, phase)

	// Verify invalid transition from terminal phase fails
	err = phases.Transition("test-team", types.PhasePlan, "should fail")
	assert.Error(t, err)
	assert.Equal(t, ErrPhaseTerminal, err)
}

// TestPhaseTransitionHistory tests that all transitions are recorded with correct metadata.
func TestPhaseTransitionHistory(t *testing.T) {
	_, _, _, phases := setupTestPhases(t, 2)

	// Perform several transitions
	transitions := []struct {
		to     types.TeamPhase
		reason string
	}{
		{types.PhasePRD, "requirements gathered"},
		{types.PhaseExec, "prd approved"},
		{types.PhaseVerify, "execution complete"},
	}

	for _, tr := range transitions {
		err := phases.Transition("test-team", tr.to, tr.reason)
		require.NoError(t, err)
	}

	// Get transition history
	history, err := phases.TransitionHistory("test-team")
	require.NoError(t, err)

	// Verify all transitions are recorded
	assert.Len(t, history, 3)

	// Verify first transition
	assert.Equal(t, types.PhasePlan, history[0].From)
	assert.Equal(t, types.PhasePRD, history[0].To)
	assert.Equal(t, "requirements gathered", history[0].Reason)
	assert.False(t, history[0].Timestamp.IsZero())

	// Verify second transition
	assert.Equal(t, types.PhasePRD, history[1].From)
	assert.Equal(t, types.PhaseExec, history[1].To)
	assert.Equal(t, "prd approved", history[1].Reason)

	// Verify third transition
	assert.Equal(t, types.PhaseExec, history[2].From)
	assert.Equal(t, types.PhaseVerify, history[2].To)
	assert.Equal(t, "execution complete", history[2].Reason)
}

// TestFixLoopBounds tests that fix loops are bounded by maxFixLoops.
func TestFixLoopBounds(t *testing.T) {
	_, _, _, phases := setupTestPhases(t, 2)

	// Transition to PhaseExec
	err := phases.Transition("test-team", types.PhasePRD, "prd ready")
	require.NoError(t, err)
	err = phases.Transition("test-team", types.PhaseExec, "exec ready")
	require.NoError(t, err)

	// Transition to PhaseVerify
	err = phases.Transition("test-team", types.PhaseVerify, "verify ready")
	require.NoError(t, err)

	// First fix loop: PhaseVerify -> PhaseFix
	err = phases.Transition("test-team", types.PhaseFix, "issues found")
	require.NoError(t, err)
	count, err := phases.FixLoopCount("test-team")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Back to exec and verify
	err = phases.Transition("test-team", types.PhaseExec, "fixing")
	require.NoError(t, err)
	err = phases.Transition("test-team", types.PhaseVerify, "re-verify")
	require.NoError(t, err)

	// Second fix loop: PhaseVerify -> PhaseFix
	err = phases.Transition("test-team", types.PhaseFix, "more issues")
	require.NoError(t, err)
	count, err = phases.FixLoopCount("test-team")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Back to exec and verify again
	err = phases.Transition("test-team", types.PhaseExec, "fixing again")
	require.NoError(t, err)
	err = phases.Transition("test-team", types.PhaseVerify, "final verify")
	require.NoError(t, err)

	// Third fix attempt should auto-transition to PhaseFailed (exceeds maxFixLoops=2)
	err = phases.Transition("test-team", types.PhaseFix, "final attempt")
	require.NoError(t, err)

	// Verify phase is now PhaseFailed (not PhaseFix)
	phase, err := phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseFailed, phase)

	// Verify fix loop count is still 2 (not incremented beyond max)
	count, err = phases.FixLoopCount("test-team")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestTaskLifecycle tests creating, claiming, completing, and failing tasks.
func TestTaskLifecycle(t *testing.T) {
	_, _, tasks, _ := setupTestPhases(t, 2)

	// Create a task
	task := &types.TeamTask{
		ID:          "task-1",
		Subject:     "Test Task",
		Description: "A test task",
	}
	err := tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	// Verify task is listed with TaskPending status
	listed, err := tasks.ListTasks("test-team")
	require.NoError(t, err)
	assert.Len(t, listed, 1)
	assert.Equal(t, types.TaskPending, listed[0].Status)

	// Claim task
	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Verify status is TaskInProgress
	claimed, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimed.Status)
	assert.NotNil(t, claimed.Claim)
	assert.Equal(t, "worker-1", claimed.Claim.WorkerID)

	// Complete task
	err = tasks.CompleteTask("test-team", "task-1", token, "task completed successfully")
	require.NoError(t, err)

	// Verify status is TaskCompleted
	completed, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskCompleted, completed.Status)
	assert.Equal(t, "task completed successfully", completed.Result)
	assert.Nil(t, completed.Claim)

	// Create another task and fail it
	task2 := &types.TeamTask{
		ID:          "task-2",
		Subject:     "Failing Task",
		Description: "A task that will fail",
	}
	err = tasks.CreateTask("test-team", task2)
	require.NoError(t, err)

	token2, err := tasks.ClaimTask("test-team", "task-2", "worker-2", 1)
	require.NoError(t, err)

	err = tasks.FailTask("test-team", "task-2", token2, "task failed due to error")
	require.NoError(t, err)

	// Verify status is TaskFailed
	failed, err := tasks.GetTask("test-team", "task-2")
	require.NoError(t, err)
	assert.Equal(t, types.TaskFailed, failed.Status)
	assert.Equal(t, "task failed due to error", failed.Result)
	assert.Nil(t, failed.Claim)
}

// TestTaskClaimNextSkipsBlocked tests that ClaimNextTask skips blocked tasks.
func TestTaskClaimNextSkipsBlocked(t *testing.T) {
	_, _, tasks, _ := setupTestPhases(t, 2)

	// Create task-1 (no dependencies)
	task1 := &types.TeamTask{
		ID:      "task-1",
		Subject: "First Task",
	}
	err := tasks.CreateTask("test-team", task1)
	require.NoError(t, err)

	// Create task-2 (depends on task-1)
	task2 := &types.TeamTask{
		ID:        "task-2",
		Subject:   "Second Task",
		DependsOn: []string{"task-1"},
	}
	err = tasks.CreateTask("test-team", task2)
	require.NoError(t, err)

	// ClaimNextTask should return task-1 (not task-2, which is blocked)
	claimed, token, err := tasks.ClaimNextTask("test-team", "worker-1")
	require.NoError(t, err)
	assert.NotNil(t, claimed)
	assert.Equal(t, "task-1", claimed.ID)
	assert.NotEmpty(t, token)

	// Complete task-1
	err = tasks.CompleteTask("test-team", "task-1", token, "done")
	require.NoError(t, err)

	// Now ClaimNextTask should return task-2 (dependency satisfied)
	claimed2, token2, err := tasks.ClaimNextTask("test-team", "worker-2")
	require.NoError(t, err)
	assert.NotNil(t, claimed2)
	assert.Equal(t, "task-2", claimed2.ID)
	assert.NotEmpty(t, token2)
}

// TestTaskLeaseExpiry tests that task leases expire and auto-release.
func TestTaskLeaseExpiry(t *testing.T) {
	_, paths := setupTestStore(t)

	// Create TaskRegistry with 1-second lease
	store := NewStore(paths)
	tasks := NewTaskRegistry(store, paths, 1)

	// Create team
	_, err := store.CreateManifest("test-team", types.TeamConfig{MaxWorkers: 1})
	require.NoError(t, err)

	// Create and claim a task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Lease Test",
	}
	err = tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)

	// Verify task is in progress
	claimed, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimed.Status)

	// Sleep for 2 seconds (lease expires after 1 second)
	time.Sleep(2 * time.Second)

	// ClaimNextTask should return the same task (lease expired, auto-released)
	nextTask, nextToken, err := tasks.ClaimNextTask("test-team", "worker-2")
	require.NoError(t, err)
	assert.NotNil(t, nextTask)
	assert.Equal(t, "task-1", nextTask.ID)
	assert.NotEmpty(t, nextToken)
	assert.NotEqual(t, token, nextToken) // Different token from new claim
}

// TestPipelinePlanToPRDToExec tests a mini-pipeline: Plan -> PRD -> Exec.
func TestPipelinePlanToPRDToExec(t *testing.T) {
	_, _, tasks, phases := setupTestPhases(t, 2)

	// Verify initial phase is PhasePlan
	phase, err := phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhasePlan, phase)

	// Create a planning task
	planTask := &types.TeamTask{
		ID:          "plan-task-1",
		Subject:     "Create Plan",
		Description: "Gather requirements",
	}
	err = tasks.CreateTask("test-team", planTask)
	require.NoError(t, err)

	// Claim and complete the planning task
	token, err := tasks.ClaimTask("test-team", "plan-task-1", "worker-1", 1)
	require.NoError(t, err)
	err = tasks.CompleteTask("test-team", "plan-task-1", token, "plan created")
	require.NoError(t, err)

	// Transition to PhasePRD
	err = phases.Transition("test-team", types.PhasePRD, "plan complete")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhasePRD, phase)

	// Create a PRD task
	prdTask := &types.TeamTask{
		ID:          "prd-task-1",
		Subject:     "Create PRD",
		Description: "Write product requirements",
	}
	err = tasks.CreateTask("test-team", prdTask)
	require.NoError(t, err)

	// Claim and complete the PRD task
	token, err = tasks.ClaimTask("test-team", "prd-task-1", "worker-2", 1)
	require.NoError(t, err)
	err = tasks.CompleteTask("test-team", "prd-task-1", token, "prd created")
	require.NoError(t, err)

	// Transition to PhaseExec
	err = phases.Transition("test-team", types.PhaseExec, "prd approved")
	require.NoError(t, err)
	phase, err = phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseExec, phase)

	// Verify transition history shows all three phases
	history, err := phases.TransitionHistory("test-team")
	require.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, types.PhasePlan, history[0].From)
	assert.Equal(t, types.PhasePRD, history[0].To)
	assert.Equal(t, types.PhasePRD, history[1].From)
	assert.Equal(t, types.PhaseExec, history[1].To)
}

// TestAllTasksTerminal tests the AllTasksTerminal predicate.
func TestAllTasksTerminal(t *testing.T) {
	_, _, tasks, phases := setupTestPhases(t, 2)

	// Initially no tasks, should be terminal
	allTerminal, err := phases.AllTasksTerminal("test-team")
	require.NoError(t, err)
	assert.True(t, allTerminal)

	// Create a pending task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Test",
	}
	err = tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	// Now should not be terminal
	allTerminal, err = phases.AllTasksTerminal("test-team")
	require.NoError(t, err)
	assert.False(t, allTerminal)

	// Claim and complete the task
	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	err = tasks.CompleteTask("test-team", "task-1", token, "done")
	require.NoError(t, err)

	// Now should be terminal again
	allTerminal, err = phases.AllTasksTerminal("test-team")
	require.NoError(t, err)
	assert.True(t, allTerminal)
}

// TestAllTasksCompleted tests the AllTasksCompleted predicate.
func TestAllTasksCompleted(t *testing.T) {
	_, _, tasks, phases := setupTestPhases(t, 2)

	// Create two tasks
	task1 := &types.TeamTask{ID: "task-1", Subject: "Task 1"}
	task2 := &types.TeamTask{ID: "task-2", Subject: "Task 2"}
	err := tasks.CreateTask("test-team", task1)
	require.NoError(t, err)
	err = tasks.CreateTask("test-team", task2)
	require.NoError(t, err)

	// Not all completed yet
	allCompleted, err := phases.AllTasksCompleted("test-team")
	require.NoError(t, err)
	assert.False(t, allCompleted)

	// Complete task-1
	token1, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	err = tasks.CompleteTask("test-team", "task-1", token1, "done")
	require.NoError(t, err)

	// Still not all completed
	allCompleted, err = phases.AllTasksCompleted("test-team")
	require.NoError(t, err)
	assert.False(t, allCompleted)

	// Complete task-2
	token2, err := tasks.ClaimTask("test-team", "task-2", "worker-2", 1)
	require.NoError(t, err)
	err = tasks.CompleteTask("test-team", "task-2", token2, "done")
	require.NoError(t, err)

	// Now all completed
	allCompleted, err = phases.AllTasksCompleted("test-team")
	require.NoError(t, err)
	assert.True(t, allCompleted)
}

// TestAnyTaskFailed tests the AnyTaskFailed predicate.
func TestAnyTaskFailed(t *testing.T) {
	_, _, tasks, phases := setupTestPhases(t, 2)

	// Create two tasks
	task1 := &types.TeamTask{ID: "task-1", Subject: "Task 1"}
	task2 := &types.TeamTask{ID: "task-2", Subject: "Task 2"}
	err := tasks.CreateTask("test-team", task1)
	require.NoError(t, err)
	err = tasks.CreateTask("test-team", task2)
	require.NoError(t, err)

	// No tasks failed yet
	anyFailed, err := phases.AnyTaskFailed("test-team")
	require.NoError(t, err)
	assert.False(t, anyFailed)

	// Fail task-1
	token1, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	err = tasks.FailTask("test-team", "task-1", token1, "error")
	require.NoError(t, err)

	// Now a task has failed
	anyFailed, err = phases.AnyTaskFailed("test-team")
	require.NoError(t, err)
	assert.True(t, anyFailed)
}

// TestTaskVersionConflict tests that version conflicts are detected.
func TestTaskVersionConflict(t *testing.T) {
	_, _, tasks, _ := setupTestPhases(t, 2)

	// Create a task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Test",
	}
	err := tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	// Claim with correct version
	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// Try to claim again with old version (should fail)
	_, err = tasks.ClaimTask("test-team", "task-1", "worker-2", 1)
	assert.Error(t, err)
	assert.Equal(t, ErrVersionConflict, err)
}

// TestTaskInvalidClaim tests that invalid claim tokens are rejected.
func TestTaskInvalidClaim(t *testing.T) {
	_, _, tasks, _ := setupTestPhases(t, 2)

	// Create and claim a task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Test",
	}
	err := tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)

	// Try to complete with invalid token
	err = tasks.CompleteTask("test-team", "task-1", "invalid-token", "done")
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidClaim, err)

	// Try to fail with invalid token
	err = tasks.FailTask("test-team", "task-1", "invalid-token", "error")
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidClaim, err)

	// Complete with valid token should work
	err = tasks.CompleteTask("test-team", "task-1", token, "done")
	require.NoError(t, err)
}

// TestRenewLease tests that task leases can be renewed.
func TestRenewLease(t *testing.T) {
	store, paths := setupTestStore(t)

	// Create TaskRegistry with 1-second lease
	tasks := NewTaskRegistry(store, paths, 1)

	// Create team
	_, err := store.CreateManifest("test-team", types.TeamConfig{MaxWorkers: 1})
	require.NoError(t, err)

	// Create and claim a task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Lease Renewal Test",
	}
	err = tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)

	// Sleep for 0.5 seconds
	time.Sleep(500 * time.Millisecond)

	// Renew lease
	err = tasks.RenewLease("test-team", "task-1", token)
	require.NoError(t, err)

	// Sleep for another 0.7 seconds (total 1.2 seconds from original claim)
	time.Sleep(700 * time.Millisecond)

	// Task should still be in progress (lease was renewed)
	claimed, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimed.Status)
}

// TestReleaseTask tests that tasks can be released back to pending.
func TestReleaseTask(t *testing.T) {
	_, _, tasks, _ := setupTestPhases(t, 2)

	// Create and claim a task
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Release Test",
	}
	err := tasks.CreateTask("test-team", task)
	require.NoError(t, err)

	token, err := tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)

	// Verify task is in progress
	claimed, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimed.Status)

	// Release task
	err = tasks.ReleaseTask("test-team", "task-1", token)
	require.NoError(t, err)

	// Verify task is back to pending
	released, err := tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskPending, released.Status)
	assert.Nil(t, released.Claim)
}

// TestArtifactStorage tests storing and retrieving artifacts in phase state.
func TestArtifactStorage(t *testing.T) {
	_, _, _, phases := setupTestPhases(t, 2)

	// Set an artifact
	err := phases.SetArtifact("test-team", "plan-doc", "/path/to/plan.md")
	require.NoError(t, err)

	// Retrieve artifact
	value, err := phases.GetArtifact("test-team", "plan-doc")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/plan.md", value)

	// Set another artifact
	err = phases.SetArtifact("test-team", "prd-doc", "/path/to/prd.md")
	require.NoError(t, err)

	// Verify both artifacts exist
	plan, err := phases.GetArtifact("test-team", "plan-doc")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/plan.md", plan)

	prd, err := phases.GetArtifact("test-team", "prd-doc")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/prd.md", prd)
}

// TestCancelPhase tests cancelling a team from any non-terminal phase.
func TestCancelPhase(t *testing.T) {
	_, _, _, phases := setupTestPhases(t, 2)

	// Transition to PhasePRD
	err := phases.Transition("test-team", types.PhasePRD, "ready for prd")
	require.NoError(t, err)

	// Cancel from PhasePRD
	err = phases.Cancel("test-team", "user cancelled")
	require.NoError(t, err)

	// Verify phase is now PhaseCancelled
	phase, err := phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseCancelled, phase)

	// Verify transition history includes cancellation
	history, err := phases.TransitionHistory("test-team")
	require.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, types.PhaseCancelled, history[1].To)
	assert.Equal(t, "user cancelled", history[1].Reason)
}
