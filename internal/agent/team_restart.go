package agent

import (
	"context"
	"fmt"
	"time"
)

// WorkerRestartOptions defines options for restarting a worker
type WorkerRestartOptions struct {
	GracePeriod   time.Duration
	PreserveState bool
	ReassignTasks bool
	MaxRetries    int
}

// WorkerRestartResult represents the result of a worker restart
type WorkerRestartResult struct {
	WorkerName      string    `json:"worker_name"`
	OldPID          int       `json:"old_pid"`
	NewPID          int       `json:"new_pid,omitempty"`
	Success         bool      `json:"success"`
	Error           string    `json:"error,omitempty"`
	RestartedAt     time.Time `json:"restarted_at"`
	ReassignedTasks []string  `json:"reassigned_tasks,omitempty"`
}

// RestartWorker attempts to restart a worker
func (r *teamCLIRunner) RestartWorker(ctx context.Context, workspace, teamName, workerName string, opts *WorkerRestartOptions) (*WorkerRestartResult, error) {
	if opts == nil {
		opts = &WorkerRestartOptions{
			GracePeriod:   5 * time.Second,
			PreserveState: true,
			ReassignTasks: true,
			MaxRetries:    3,
		}
	}

	result := &WorkerRestartResult{
		WorkerName:  workerName,
		RestartedAt: time.Now(),
	}

	// Get current worker status
	var heartbeatResp struct {
		Worker    string `json:"worker"`
		Heartbeat *struct {
			PID        int       `json:"pid"`
			LastTurnAt time.Time `json:"last_turn_at"`
			TurnCount  int       `json:"turn_count"`
			Alive      bool      `json:"alive"`
		} `json:"heartbeat"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-worker-heartbeat", map[string]string{
		"team_name": teamName,
		"worker":    workerName,
	}, &heartbeatResp); err != nil {
		return nil, fmt.Errorf("read worker heartbeat: %w", err)
	}

	if heartbeatResp.Heartbeat != nil {
		result.OldPID = heartbeatResp.Heartbeat.PID
	}

	// Get worker's current tasks if we need to reassign them
	var tasksToReassign []string
	if opts.ReassignTasks {
		tasks, err := r.GetTasksByWorker(ctx, workspace, teamName, workerName)
		if err != nil {
			r.logger.Warn("failed to get worker tasks for reassignment",
				"team", teamName,
				"worker", workerName,
				"error", err,
			)
		} else {
			for _, task := range tasks {
				if task.Status == "in_progress" {
					tasksToReassign = append(tasksToReassign, task.ID)
				}
			}
		}
	}

	// Write shutdown request for the worker
	if err := r.writeShutdownRequest(ctx, workspace, teamName, workerName, "restart"); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to write shutdown request: %v", err)
		return result, nil
	}

	// Wait for shutdown acknowledgment with grace period
	shutdownCtx, cancel := context.WithTimeout(ctx, opts.GracePeriod)
	defer cancel()

	ackReceived := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-shutdownCtx.Done():
			// Grace period expired
			r.logger.Warn("worker did not acknowledge shutdown within grace period",
				"team", teamName,
				"worker", workerName,
				"grace_period", opts.GracePeriod,
			)
			break
		case <-ticker.C:
			var ackResp struct {
				Worker string `json:"worker"`
				Ack    *struct {
					Status    string    `json:"status"`
					Reason    string    `json:"reason,omitempty"`
					UpdatedAt time.Time `json:"updated_at,omitempty"`
				} `json:"ack"`
			}

			if err := r.runTeamAPI(ctx, workspace, "read-shutdown-ack", map[string]string{
				"team_name": teamName,
				"worker":    workerName,
			}, &ackResp); err == nil && ackResp.Ack != nil {
				if ackResp.Ack.Status == "accept" {
					ackReceived = true
					break
				}
			}
		}

		if ackReceived {
			break
		}
	}

	// Reassign tasks if requested
	if opts.ReassignTasks && len(tasksToReassign) > 0 {
		for _, taskID := range tasksToReassign {
			// Release the task claim
			task, err := r.ReadTask(ctx, workspace, teamName, taskID)
			if err != nil {
				r.logger.Warn("failed to read task for reassignment",
					"team", teamName,
					"task_id", taskID,
					"error", err,
				)
				continue
			}

			if task.Claim != nil && task.Claim.Owner == workerName {
				if _, err := r.ReleaseTaskClaim(ctx, workspace, teamName, taskID, task.Claim.Token, workerName); err != nil {
					r.logger.Warn("failed to release task claim for reassignment",
						"team", teamName,
						"task_id", taskID,
						"error", err,
					)
					continue
				}
				result.ReassignedTasks = append(result.ReassignedTasks, taskID)
			}
		}
	}

	// Note: Actual worker restart would require integration with the team CLI
	// to spawn a new worker process. For now, we mark this as a placeholder.
	result.Success = false
	result.Error = "worker restart requires team CLI integration"

	return result, nil
}

// writeShutdownRequest writes a shutdown request for a worker
func (r *teamCLIRunner) writeShutdownRequest(ctx context.Context, workspace, teamName, worker, requestedBy string) error {
	input := map[string]string{
		"team_name":    teamName,
		"worker":       worker,
		"requested_by": requestedBy,
	}

	if err := r.runTeamAPI(ctx, workspace, "write-shutdown-request", input, nil); err != nil {
		return fmt.Errorf("write shutdown request: %w", err)
	}

	return nil
}

// RestartDeadWorkers identifies and attempts to restart all dead workers
func (r *teamCLIRunner) RestartDeadWorkers(ctx context.Context, workspace, teamName string, maxHeartbeatAge time.Duration) ([]*WorkerRestartResult, error) {
	health, err := r.GetTeamHealth(ctx, workspace, teamName, maxHeartbeatAge)
	if err != nil {
		return nil, fmt.Errorf("get team health: %w", err)
	}

	var results []*WorkerRestartResult

	for _, workerReport := range health.WorkerReports {
		if workerReport.Status == "dead" {
			result, err := r.RestartWorker(ctx, workspace, teamName, workerReport.WorkerName, nil)
			if err != nil {
				r.logger.Error("failed to restart dead worker",
					"team", teamName,
					"worker", workerReport.WorkerName,
					"error", err,
				)
				continue
			}
			results = append(results, result)
		}
	}

	return results, nil
}

// QuarantineWorker marks a worker as quarantined due to repeated errors
func (r *teamCLIRunner) QuarantineWorker(ctx context.Context, workspace, teamName, workerName, reason string) error {
	// Update worker status to quarantined
	event := &TeamEvent{
		Type:   "worker_state_changed",
		Worker: workerName,
		State:  "quarantined",
		Reason: reason,
	}

	if _, err := r.AppendEvent(ctx, workspace, teamName, event); err != nil {
		return fmt.Errorf("append quarantine event: %w", err)
	}

	r.logger.Info("worker quarantined",
		"team", teamName,
		"worker", workerName,
		"reason", reason,
	)

	return nil
}
