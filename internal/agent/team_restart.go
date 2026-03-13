package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// WorkerRestartOptions defines options for restarting a worker.
type WorkerRestartOptions struct {
	GracePeriod   time.Duration
	PreserveState bool
	ReassignTasks bool
	MaxRetries    int
}

// WorkerRestartResult represents the result of a worker restart.
type WorkerRestartResult struct {
	WorkerName      string    `json:"worker_name"`
	OldPID          int       `json:"old_pid"`
	Success         bool      `json:"success"`
	Error           string    `json:"error,omitempty"`
	RestartedAt     time.Time `json:"restarted_at"`
	ReassignedTasks []string  `json:"reassigned_tasks,omitempty"`
}

// RestartWorker attempts to gracefully restart a worker by sending a shutdown
// request via the CLI API and reassigning its in-progress tasks.
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

	// Get current worker PID from heartbeat.
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

	// Collect in-progress tasks to reassign.
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
				if task.Status == types.TaskInProgress {
					tasksToReassign = append(tasksToReassign, task.ID)
				}
			}
		}
	}

	// Send shutdown request.
	if err := r.writeShutdownRequest(ctx, workspace, teamName, workerName, "restart"); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to write shutdown request: %v", err)
		return result, nil
	}

	// Wait for shutdown acknowledgment within grace period.
	shutdownCtx, cancel := context.WithTimeout(ctx, opts.GracePeriod)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

waitLoop:
	for {
		select {
		case <-shutdownCtx.Done():
			r.logger.Warn("worker did not acknowledge shutdown within grace period",
				"team", teamName,
				"worker", workerName,
				"grace_period", opts.GracePeriod,
			)
			break waitLoop
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
					break waitLoop
				}
			}
		}
	}

	// Reassign tasks by releasing their claims.
	if opts.ReassignTasks && len(tasksToReassign) > 0 {
		for _, taskID := range tasksToReassign {
			task, err := r.ReadTask(ctx, workspace, teamName, taskID)
			if err != nil {
				r.logger.Warn("failed to read task for reassignment",
					"team", teamName,
					"task_id", taskID,
					"error", err,
				)
				continue
			}

			if task.Claim != nil && task.Claim.WorkerID == workerName {
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

	result.Success = true
	return result, nil
}

// writeShutdownRequest writes a shutdown request for a worker.
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

// RestartDeadWorkers identifies and attempts to restart all dead workers.
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

// QuarantineWorker marks a worker as quarantined due to repeated errors.
func (r *teamCLIRunner) QuarantineWorker(ctx context.Context, workspace, teamName, workerName, reason string) error {
	event := &types.TeamEvent{
		Type:     "worker_state_changed",
		TeamName: teamName,
		Data: map[string]interface{}{
			"worker": workerName,
			"state":  "quarantined",
			"reason": reason,
		},
		Timestamp: time.Now(),
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
