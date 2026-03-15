package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// cliTask is the JSON shape returned by the team CLI task API.
type cliTask struct {
	ID                 string        `json:"id"`
	Subject            string        `json:"subject"`
	Description        string        `json:"description"`
	Status             string        `json:"status"`
	RequiresCodeChange bool          `json:"requires_code_change,omitempty"`
	Role               string        `json:"role,omitempty"`
	Owner              string        `json:"owner,omitempty"`
	Result             string        `json:"result,omitempty"`
	Error              string        `json:"error,omitempty"`
	BlockedBy          []string      `json:"blocked_by,omitempty"`
	DependsOn          []string      `json:"depends_on,omitempty"`
	Version            int           `json:"version"`
	Claim              *cliTaskClaim `json:"claim,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	CompletedAt        time.Time     `json:"completed_at,omitempty"`
}

type cliTaskClaim struct {
	Owner           string    `json:"owner"`
	Token           string    `json:"token"`
	LeasedUntil     time.Time `json:"leased_until"`
	ExpectedVersion int       `json:"expected_version,omitempty"`
}

func (t *cliTask) toTeamTask() types.TeamTask {
	// Use CreatedAt as fallback for UpdatedAt when CompletedAt is not set,
	// since the CLI response doesn't provide a dedicated updated_at field.
	updatedAt := t.CreatedAt
	if !t.CompletedAt.IsZero() {
		updatedAt = t.CompletedAt
	}

	task := types.TeamTask{
		ID:          t.ID,
		Subject:     t.Subject,
		Description: t.Description,
		Status:      types.TeamTaskStatus(t.Status),
		BlockedBy:   t.BlockedBy,
		DependsOn:   t.DependsOn,
		Version:     t.Version,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   updatedAt,
		Result:      t.Result,
	}
	if t.Claim != nil {
		task.Claim = &types.TaskClaim{
			WorkerID:    t.Claim.Owner,
			Token:       t.Claim.Token,
			LeasedAt:    t.CreatedAt,
			LeasedUntil: t.Claim.LeasedUntil,
		}
	}
	return task
}

// ClaimTaskResult represents the result of claiming a task.
type ClaimTaskResult struct {
	OK           bool            `json:"ok"`
	Task         *types.TeamTask `json:"-"`
	RawTask      *cliTask        `json:"task,omitempty"`
	ClaimToken   string          `json:"claimToken,omitempty"`
	Error        string          `json:"error,omitempty"`
	Dependencies []string        `json:"dependencies,omitempty"`
}

// TransitionTaskResult represents the result of transitioning a task.
type TransitionTaskResult struct {
	OK      bool     `json:"ok"`
	RawTask *cliTask `json:"task,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// CreateTask creates a new task in the team.
func (r *teamCLIRunner) CreateTask(ctx context.Context, workspace, teamName string, task *types.TeamTask) (*types.TeamTask, error) {
	input := map[string]interface{}{
		"team_name":   teamName,
		"subject":     task.Subject,
		"description": task.Description,
	}

	if task.Claim != nil && task.Claim.WorkerID != "" {
		input["owner"] = task.Claim.WorkerID
	}
	if len(task.BlockedBy) > 0 {
		input["blocked_by"] = task.BlockedBy
	}

	var resp struct {
		Task cliTask `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "create-task", input, &resp); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	result := resp.Task.toTeamTask()
	return &result, nil
}

// ReadTask retrieves a task by ID.
func (r *teamCLIRunner) ReadTask(ctx context.Context, workspace, teamName, taskID string) (*types.TeamTask, error) {
	var resp struct {
		Task cliTask `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-task", map[string]string{
		"team_name": teamName,
		"task_id":   taskID,
	}, &resp); err != nil {
		return nil, fmt.Errorf("read task: %w", err)
	}

	result := resp.Task.toTeamTask()
	return &result, nil
}

// UpdateTask updates mutable fields of a task.
func (r *teamCLIRunner) UpdateTask(ctx context.Context, workspace, teamName, taskID string, updates map[string]interface{}) (*types.TeamTask, error) {
	input := make(map[string]interface{}, len(updates)+2)
	for k, v := range updates {
		input[k] = v
	}
	input["team_name"] = teamName
	input["task_id"] = taskID

	var resp struct {
		Task cliTask `json:"task"`
	}

	if err := r.runTeamAPI(ctx, workspace, "update-task", input, &resp); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	result := resp.Task.toTeamTask()
	return &result, nil
}

// ClaimTask attempts to claim a task for a worker.
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

	if result.RawTask != nil {
		task := result.RawTask.toTeamTask()
		result.Task = &task
	}
	return &result, nil
}

// TransitionTaskStatus transitions a task from one status to another.
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

// ReleaseTaskClaim releases a claim on a task.
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

// ListAllTasks returns all tasks in the team.
func (r *teamCLIRunner) ListAllTasks(ctx context.Context, workspace, teamName string) ([]types.TeamTask, error) {
	var resp struct {
		Count int       `json:"count"`
		Tasks []cliTask `json:"tasks"`
	}

	if err := r.runTeamAPI(ctx, workspace, "list-tasks", map[string]string{
		"team_name": teamName,
	}, &resp); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	tasks := make([]types.TeamTask, len(resp.Tasks))
	for i, t := range resp.Tasks {
		tasks[i] = t.toTeamTask()
	}
	return tasks, nil
}

// GetTasksByStatus returns tasks filtered by status.
func (r *teamCLIRunner) GetTasksByStatus(ctx context.Context, workspace, teamName, status string) ([]types.TeamTask, error) {
	allTasks, err := r.ListAllTasks(ctx, workspace, teamName)
	if err != nil {
		return nil, err
	}

	var filtered []types.TeamTask
	for _, task := range allTasks {
		if string(task.Status) == status {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}

// GetTasksByWorker returns tasks assigned to a specific worker.
func (r *teamCLIRunner) GetTasksByWorker(ctx context.Context, workspace, teamName, worker string) ([]types.TeamTask, error) {
	allTasks, err := r.ListAllTasks(ctx, workspace, teamName)
	if err != nil {
		return nil, err
	}

	var filtered []types.TeamTask
	for _, task := range allTasks {
		if task.Claim != nil && task.Claim.WorkerID == worker {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}
