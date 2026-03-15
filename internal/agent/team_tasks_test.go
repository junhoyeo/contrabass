package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLITaskConversion(t *testing.T) {
	now := time.Now()
	ct := &cliTask{
		ID:          "1",
		Subject:     "Implement feature X",
		Description: "Add support for feature X with tests",
		Status:      "in_progress",
		Owner:       "worker-1",
		Version:     2,
		Claim: &cliTaskClaim{
			Owner:       "worker-1",
			Token:       "abc123",
			LeasedUntil: now.Add(5 * time.Minute),
		},
		CreatedAt: now,
	}

	task := ct.toTeamTask()

	assert.Equal(t, ct.ID, task.ID)
	assert.Equal(t, ct.Subject, task.Subject)
	assert.Equal(t, ct.Status, string(task.Status))
	require.NotNil(t, task.Claim)
	assert.Equal(t, ct.Claim.Owner, task.Claim.WorkerID)
	assert.Equal(t, ct.Claim.Token, task.Claim.Token)
}

func TestTeamTaskJSON(t *testing.T) {
	now := time.Now()
	task := types.TeamTask{
		ID:          "1",
		Subject:     "Implement feature X",
		Description: "Add support for feature X with tests",
		Status:      types.TaskInProgress,
		Version:     2,
		Claim: &types.TaskClaim{
			WorkerID: "worker-1",
			Token:    "abc123",
			LeasedAt: now,
		},
		CreatedAt: now,
	}

	data, err := json.Marshal(task)
	require.NoError(t, err)

	var decoded types.TeamTask
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, task.ID, decoded.ID)
	assert.Equal(t, task.Version, decoded.Version)
	require.NotNil(t, decoded.Claim)
	assert.Equal(t, task.Claim.WorkerID, decoded.Claim.WorkerID)
}

func TestClaimTaskResultJSON(t *testing.T) {
	result := &ClaimTaskResult{
		OK:         true,
		ClaimToken: "token123",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded ClaimTaskResult
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, result.OK, decoded.OK)
	assert.Equal(t, result.ClaimToken, decoded.ClaimToken)
}

func TestTaskWithDependencies(t *testing.T) {
	task := types.TeamTask{
		ID:          "2",
		Subject:     "Dependent task",
		Description: "Task that depends on others",
		Status:      types.TaskPending,
		BlockedBy:   []string{"1"},
		DependsOn:   []string{"1"},
		Version:     1,
		CreatedAt:   time.Now(),
	}

	data, err := json.Marshal(task)
	require.NoError(t, err)

	var decoded types.TeamTask
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, len(task.BlockedBy), len(decoded.BlockedBy))
	assert.Equal(t, len(task.DependsOn), len(decoded.DependsOn))
}

func TestTeamCLIRunner_CreateTask(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	task, err := runner.CreateTask(context.Background(), workspace, "test-team", &types.TeamTask{
		Subject:     "created task",
		Description: "test",
	})
	require.NoError(t, err)

	assert.Equal(t, "new-task-1", task.ID)
	assert.Equal(t, "pending", string(task.Status))
	assert.Equal(t, "created task", task.Subject)
}

func TestTeamCLIRunner_ReadTask(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	task, err := runner.ReadTask(context.Background(), workspace, "test-team", "1")
	require.NoError(t, err)

	assert.Equal(t, "1", task.ID)
	assert.Equal(t, "Worker 1", task.Subject)
	assert.Equal(t, types.TaskInProgress, task.Status)
}

func TestTeamCLIRunner_ListAllTasks(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	tasks, err := runner.ListAllTasks(context.Background(), workspace, "test-team")
	require.NoError(t, err)

	require.Len(t, tasks, 1)
	assert.Equal(t, "1", tasks[0].ID)
	assert.Equal(t, types.TaskInProgress, tasks[0].Status)
}

func TestTeamCLIRunner_GetTasksByStatus(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	tasks, err := runner.GetTasksByStatus(context.Background(), workspace, "test-team", "in_progress")
	require.NoError(t, err)

	require.Len(t, tasks, 1)
	assert.Equal(t, types.TaskInProgress, tasks[0].Status)

	none, err := runner.GetTasksByStatus(context.Background(), workspace, "test-team", "blocked")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestTeamCLIRunner_UpdateTask(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	task, err := runner.UpdateTask(context.Background(), workspace, "test-team", "1", map[string]interface{}{"subject": "updated"})
	require.NoError(t, err)

	assert.Equal(t, "1", task.ID)
	assert.Equal(t, "updated task", task.Subject)
	assert.Equal(t, 2, task.Version)
}

func TestTeamCLIRunner_ClaimTask(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	expected := 2
	result, err := runner.ClaimTask(context.Background(), workspace, "test-team", "1", "worker-1", &expected)
	require.NoError(t, err)

	assert.True(t, result.OK)
	assert.Equal(t, "token-001", result.ClaimToken)
	require.NotNil(t, result.Task)
	assert.Equal(t, types.TaskInProgress, result.Task.Status)
	require.NotNil(t, result.Task.Claim)
	assert.Equal(t, "worker-1", result.Task.Claim.WorkerID)
}

func TestTeamCLIRunner_TransitionTaskStatus(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	resultText := "done"
	transition, err := runner.TransitionTaskStatus(context.Background(), workspace, "test-team", "1", "pending", "in_progress", "token-001", &resultText, nil)
	require.NoError(t, err)

	assert.True(t, transition.OK)
	require.NotNil(t, transition.RawTask)
	assert.Equal(t, "transitioned task", transition.RawTask.Subject)
}

func TestTeamCLIRunner_ReleaseTaskClaim(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	result, err := runner.ReleaseTaskClaim(context.Background(), workspace, "test-team", "1", "token-001", "worker-1")
	require.NoError(t, err)

	assert.True(t, result.OK)
	require.NotNil(t, result.RawTask)
	assert.Equal(t, "released task", result.RawTask.Subject)
}

func TestTeamCLIRunner_GetTasksByWorker(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	tasks, err := runner.GetTasksByWorker(context.Background(), workspace, "test-team", "worker-1")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}
