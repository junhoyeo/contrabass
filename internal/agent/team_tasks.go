package agent

import (
	"context"
	"fmt"
	"time"
)

// TaskClaim represents a claim on a task by a worker
type TaskClaim struct {
	Owner           string    `json:"owner"`
	Token           string    `json:"token"`
	LeasedUntil     time.Time `json:"leased_until"`
	ExpectedVersion int       `json:"expected_version,omitempty"`
}

// TaskV2 represents a task with versioning support
type TaskV2 struct {
	ID                 string     `json:"id"`
	Subject            string     `json:"subject"`
	Description        string     `json:"description"`
	Status             string     `json:"status"`
	RequiresCodeChange bool       `json:"requires_code_change,omitempty"`
	Role               string     `json:"role,omitempty"`
	Owner              string     `json:"owner,omitempty"`
	Result             string     `json:"result,omitempty"`
	Error              string     `json:"error,omitempty"`
	BlockedBy          []string   `json:"blocked_by,omitempty"`
	DependsOn          []string   `json:"depends_on,omitempty"`
	Version            int        `json:"version"`
	Claim              *TaskClaim `json:"claim,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	CompletedAt        time.Time  `json:"completed_at,omitempty"`
}

// ClaimTaskResult represents the result of claiming a task
type ClaimTaskResult struct {
	OK           bool     `json:"ok"`
	Task         *TaskV2  `json:"task,omitempty"`
	ClaimToken   string   `json:"claimToken,omitempty"`
	Error        string   `json:"error,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// TransitionTaskResult represents the result of transitioning a task
type TransitionTaskResult struct {
	OK    bool    `json:"ok"`
	Task  *TaskV2 `json:"task,omitempty"`
	Error string  `json:"error,omitempty"`
}

// CreateTask creates a new task in the team
func (r *teamCLIRunner) CreateTask(ctx context.Context, workspace, teamName string, task *TaskV2) (*TaskV2, error) {
	input := map[string]interface{}{
		"team_name":   teamName,
		"subject":     task.Subject,
		"description": task.Description,
	}

	if task.Owner != "" {
		input["owner"] = task.Owner
	}
	if len(task.BlockedBy) > 0 {
		input["blocked_by"] = task.BlockedBy
	}
	if task.RequiresCodeChange {
		input["requires_code_change"] = true
	}
	if task.Role != "" {
		input["role"] = task.Role
	}

	var resp struct {
		Task TaskV2 `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "create-task", input, &resp); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	return &resp.Task, nil
}

// ReadTask retrieves a task by ID
func (r *teamCLIRunner) ReadTask(ctx context.Context, workspace, teamName, taskID string) (*TaskV2, error) {
	var resp struct {
		Task TaskV2 `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-task", map[string]string{
		"team_name": teamName,
		"task_id":   taskID,
	}, &resp); err != nil {
		return nil, fmt.Errorf("read task: %w", err)
	}

	return &resp.Task, nil
}

// UpdateTask updates mutable fields of a task
func (r *teamCLIRunner) UpdateTask(ctx context.Context, workspace, teamName, taskID string, updates map[string]interface{}) (*TaskV2, error) {
	input := map[string]interface{}{
		"team_name": teamName,
		"task_id":   taskID,
	}

	// Merge updates into input
	for k, v := range updates {
		input[k] = v
	}

	var resp struct {
		Task TaskV2 `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "update-task", input, &resp); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	return &resp.Task, nil
}

// ClaimTask attempts to claim a task for a worker
func (r *teamCLIRunner) ClaimTask(ctx context.Context, workspace, teamName, taskID, worker string, expectedVersion *int) (*ClaimTaskResult, error) {
	input := map[string]interface{}{
		"team_name": teamName,
		"task_id":   taskID,
		"worker":    worker,
	}

	if expectedVersion != nil {
		input["expected_version"] = *expectedVersion
	}

	var result ClaimTaskResult

	if err := r.runTeamAPI(ctx, workspace, "claim-task", input, &result); err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}

	return &result, nil
}

// TransitionTaskStatus transitions a task from one status to another
func (r *teamCLIRunner) TransitionTaskStatus(ctx context.Context, workspace, teamName, taskID, from, to, claimToken string, result, errorMsg *string) (*TransitionTaskResult, error) {
	input := map[string]interface{}{
		"team_name":   teamName,
		"task_id":     taskID,
		"from":        from,
		"to":          to,
		"claim_token": claimToken,
	}

	if result != nil {
		input["result"] = *result
	}
	if errorMsg != nil {
		input["error"] = *errorMsg
	}

	var transitionResult TransitionTaskResult

	if err := r.runTeamAPI(ctx, workspace, "transition-task-status", input, &transitionResult); err != nil {
		return nil, fmt.Errorf("transition task status: %w", err)
	}

	return &transitionResult, nil
}

// ReleaseTaskClaim releases a claim on a task
func (r *teamCLIRunner) ReleaseTaskClaim(ctx context.Context, workspace, teamName, taskID, claimToken, worker string) (*TransitionTaskResult, error) {
	input := map[string]string{
		"team_name":   teamName,
		"task_id":     taskID,
		"claim_token": claimToken,
		"worker":      worker,
	}

	var result TransitionTaskResult

	if err := r.runTeamAPI(ctx, workspace, "release-task-claim", input, &result); err != nil {
		return nil, fmt.Errorf("release task claim: %w", err)
	}

	return &result, nil
}

// ListAllTasks returns all tasks in the team
func (r *teamCLIRunner) ListAllTasks(ctx context.Context, workspace, teamName string) ([]TaskV2, error) {
	var resp struct {
		Count int      `json:"count"`
		Tasks []TaskV2 `json:"tasks"`
	}

	if err := r.runTeamAPI(ctx, workspace, "list-tasks", map[string]string{
		"team_name": teamName,
	}, &resp); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	return resp.Tasks, nil
}

// GetTasksByStatus returns tasks filtered by status
func (r *teamCLIRunner) GetTasksByStatus(ctx context.Context, workspace, teamName, status string) ([]TaskV2, error) {
	allTasks, err := r.ListAllTasks(ctx, workspace, teamName)
	if err != nil {
		return nil, err
	}

	var filtered []TaskV2
	for _, task := range allTasks {
		if task.Status == status {
			filtered = append(filtered, task)
		}
	}

	return filtered, nil
}

// GetTasksByWorker returns tasks assigned to a specific worker
func (r *teamCLIRunner) GetTasksByWorker(ctx context.Context, workspace, teamName, worker string) ([]TaskV2, error) {
	allTasks, err := r.ListAllTasks(ctx, workspace, teamName)
	if err != nil {
		return nil, err
	}

	var filtered []TaskV2
	for _, task := range allTasks {
		if task.Owner == worker {
			filtered = append(filtered, task)
		}
	}

	return filtered, nil
}
