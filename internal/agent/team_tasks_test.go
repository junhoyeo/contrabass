package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
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

	if task.ID != ct.ID {
		t.Errorf("ID mismatch: got %s, want %s", task.ID, ct.ID)
	}
	if task.Subject != ct.Subject {
		t.Errorf("Subject mismatch: got %s, want %s", task.Subject, ct.Subject)
	}
	if string(task.Status) != ct.Status {
		t.Errorf("Status mismatch: got %s, want %s", task.Status, ct.Status)
	}
	if task.Claim == nil {
		t.Fatal("Claim is nil")
	}
	if task.Claim.WorkerID != ct.Claim.Owner {
		t.Errorf("Claim WorkerID mismatch: got %s, want %s", task.Claim.WorkerID, ct.Claim.Owner)
	}
	if task.Claim.Token != ct.Claim.Token {
		t.Errorf("Claim Token mismatch: got %s, want %s", task.Claim.Token, ct.Claim.Token)
	}
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
	if err != nil {
		t.Fatalf("Failed to marshal task: %v", err)
	}

	var decoded types.TeamTask
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal task: %v", err)
	}

	if decoded.ID != task.ID {
		t.Errorf("ID mismatch: got %s, want %s", decoded.ID, task.ID)
	}
	if decoded.Version != task.Version {
		t.Errorf("Version mismatch: got %d, want %d", decoded.Version, task.Version)
	}
	if decoded.Claim == nil {
		t.Fatal("Claim is nil")
	}
	if decoded.Claim.WorkerID != task.Claim.WorkerID {
		t.Errorf("Claim WorkerID mismatch: got %s, want %s", decoded.Claim.WorkerID, task.Claim.WorkerID)
	}
}

func TestClaimTaskResultJSON(t *testing.T) {
	result := &ClaimTaskResult{
		OK:         true,
		ClaimToken: "token123",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal claim result: %v", err)
	}

	var decoded ClaimTaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal claim result: %v", err)
	}

	if decoded.OK != result.OK {
		t.Errorf("OK mismatch: got %v, want %v", decoded.OK, result.OK)
	}
	if decoded.ClaimToken != result.ClaimToken {
		t.Errorf("ClaimToken mismatch: got %s, want %s", decoded.ClaimToken, result.ClaimToken)
	}
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
	if err != nil {
		t.Fatalf("Failed to marshal task: %v", err)
	}

	var decoded types.TeamTask
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal task: %v", err)
	}

	if len(decoded.BlockedBy) != len(task.BlockedBy) {
		t.Errorf("BlockedBy length mismatch: got %d, want %d", len(decoded.BlockedBy), len(task.BlockedBy))
	}
	if len(decoded.DependsOn) != len(task.DependsOn) {
		t.Errorf("DependsOn length mismatch: got %d, want %d", len(decoded.DependsOn), len(task.DependsOn))
	}
}
