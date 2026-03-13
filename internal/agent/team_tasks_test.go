package agent

import (
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
