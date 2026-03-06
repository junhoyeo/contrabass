package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

var (
	ErrTaskNotFound     = errors.New("task not found")
	ErrVersionConflict  = errors.New("version conflict: task was modified concurrently")
	ErrTaskNotClaimable = errors.New("task is not claimable")
	ErrTaskBlocked      = errors.New("task is blocked by unfinished dependencies")
	ErrInvalidClaim     = errors.New("invalid claim token")
)

type TaskRegistry struct {
	store        *Store
	paths        *Paths
	leaseSeconds int
}

func NewTaskRegistry(store *Store, paths *Paths, leaseSeconds int) *TaskRegistry {
	return &TaskRegistry{
		store:        store,
		paths:        paths,
		leaseSeconds: leaseSeconds,
	}
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (r *TaskRegistry) CreateTask(teamName string, task *types.TeamTask) error {
	if task == nil {
		return errors.New("task is required")
	}
	if task.ID == "" {
		return errors.New("task ID is required")
	}

	now := time.Now()
	task.Status = types.TaskPending
	task.Claim = nil
	task.Version = 1
	task.CreatedAt = now
	task.UpdatedAt = now

	path := r.paths.TaskPath(teamName, task.ID)
	if err := r.store.WriteJSON(path, task); err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (r *TaskRegistry) GetTask(teamName, taskID string) (*types.TeamTask, error) {
	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)
	return &task, nil
}

func (r *TaskRegistry) ListTasks(teamName string) ([]types.TeamTask, error) {
	dir := r.paths.TasksDir(teamName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.TeamTask{}, nil
		}
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	tasks := make([]types.TeamTask, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		var task types.TeamTask
		path := filepath.Join(dir, entry.Name())
		if err := r.store.ReadJSON(path, &task); err != nil {
			continue
		}

		r.applyLeaseExpiry(&task)
		tasks = append(tasks, task)
	}

	return tasks, nil
}

func (r *TaskRegistry) ClaimTask(teamName, taskID, workerID string, expectedVersion int) (string, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return "", ErrTaskNotFound
		}
		return "", fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)

	if task.Version != expectedVersion {
		return "", ErrVersionConflict
	}

	if !r.isClaimable(&task) {
		return "", ErrTaskNotClaimable
	}

	blocked, err := r.isBlocked(teamName, &task)
	if err != nil {
		return "", fmt.Errorf("check dependencies: %w", err)
	}
	if blocked {
		return "", ErrTaskBlocked
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	task.Status = types.TaskInProgress
	task.Claim = &types.TaskClaim{
		WorkerID: workerID,
		Token:    token,
		LeasedAt: now,
	}
	task.Version++
	task.UpdatedAt = now

	if err := r.store.WriteJSON(path, &task); err != nil {
		return "", fmt.Errorf("write claimed task: %w", err)
	}

	return token, nil
}

func (r *TaskRegistry) RenewLease(teamName, taskID, token string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)
	if task.Claim == nil || task.Claim.Token != token {
		return ErrInvalidClaim
	}

	now := time.Now()
	task.Claim.LeasedAt = now
	task.UpdatedAt = now
	task.Version++

	if err := r.store.WriteJSON(path, &task); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (r *TaskRegistry) CompleteTask(teamName, taskID, token, result string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)
	if task.Claim == nil || task.Claim.Token != token {
		return ErrInvalidClaim
	}

	task.Status = types.TaskCompleted
	task.Result = result
	task.Claim = nil
	task.Version++
	task.UpdatedAt = time.Now()

	if err := r.store.WriteJSON(path, &task); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (r *TaskRegistry) FailTask(teamName, taskID, token, reason string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)
	if task.Claim == nil || task.Claim.Token != token {
		return ErrInvalidClaim
	}

	task.Status = types.TaskFailed
	task.Result = reason
	task.Claim = nil
	task.Version++
	task.UpdatedAt = time.Now()

	if err := r.store.WriteJSON(path, &task); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (r *TaskRegistry) ReleaseTask(teamName, taskID, token string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	path := r.paths.TaskPath(teamName, taskID)
	var task types.TeamTask
	if err := r.store.ReadJSON(path, &task); err != nil {
		if os.IsNotExist(err) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("read task: %w", err)
	}

	r.applyLeaseExpiry(&task)
	if task.Claim == nil || task.Claim.Token != token {
		return ErrInvalidClaim
	}

	task.Status = types.TaskPending
	task.Claim = nil
	task.Version++
	task.UpdatedAt = time.Now()

	if err := r.store.WriteJSON(path, &task); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (r *TaskRegistry) ClaimNextTask(teamName, workerID string) (*types.TeamTask, string, error) {
	tasks, err := r.ListTasks(teamName)
	if err != nil {
		return nil, "", err
	}

	for _, task := range tasks {
		if !r.isClaimable(&task) {
			continue
		}

		token, claimErr := r.ClaimTask(teamName, task.ID, workerID, task.Version)
		if claimErr != nil {
			if errors.Is(claimErr, ErrVersionConflict) || errors.Is(claimErr, ErrTaskNotClaimable) || errors.Is(claimErr, ErrTaskBlocked) {
				continue
			}
			return nil, "", claimErr
		}

		claimedTask, getErr := r.GetTask(teamName, task.ID)
		if getErr != nil {
			return nil, "", getErr
		}
		return claimedTask, token, nil
	}

	return nil, "", ErrTaskNotClaimable
}

func (r *TaskRegistry) isBlocked(teamName string, task *types.TeamTask) (bool, error) {
	if len(task.DependsOn) == 0 {
		return false, nil
	}

	for _, depID := range task.DependsOn {
		depPath := r.paths.TaskPath(teamName, depID)
		var dep types.TeamTask
		if err := r.store.ReadJSON(depPath, &dep); err != nil {
			if os.IsNotExist(err) {
				return true, nil
			}
			return false, err
		}

		r.applyLeaseExpiry(&dep)
		if dep.Status != types.TaskCompleted {
			return true, nil
		}
	}

	return false, nil
}

func (r *TaskRegistry) isClaimable(task *types.TeamTask) bool {
	r.applyLeaseExpiry(task)
	return task.Status == types.TaskPending
}

func (r *TaskRegistry) applyLeaseExpiry(task *types.TeamTask) {
	if task.Status != types.TaskInProgress || task.Claim == nil {
		return
	}
	if r.leaseSeconds <= 0 {
		return
	}

	leaseDuration := time.Duration(r.leaseSeconds) * time.Second
	if time.Since(task.Claim.LeasedAt) <= leaseDuration {
		return
	}

	task.Status = types.TaskPending
	task.Claim = nil
	task.Version++
	task.UpdatedAt = time.Now()
}
