package agent

import (
	"context"
	"fmt"
	"time"
)

// WorkerHealthReport represents the health status of a team worker
type WorkerHealthReport struct {
	WorkerName        string    `json:"worker_name"`
	IsAlive           bool      `json:"is_alive"`
	HeartbeatAge      *int64    `json:"heartbeat_age_ms,omitempty"` // milliseconds since last heartbeat
	Status            string    `json:"status"`                     // active, idle, dead, quarantined, unknown
	CurrentTaskID     string    `json:"current_task_id,omitempty"`
	TotalTurns        int       `json:"total_turns"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	LastTurnAt        time.Time `json:"last_turn_at,omitempty"`
}

// TeamHealthSummary represents the overall health of a team
type TeamHealthSummary struct {
	TeamName           string               `json:"team_name"`
	TotalWorkers       int                  `json:"total_workers"`
	HealthyWorkers     int                  `json:"healthy_workers"`
	DeadWorkers        int                  `json:"dead_workers"`
	QuarantinedWorkers int                  `json:"quarantined_workers"`
	WorkerReports      []WorkerHealthReport `json:"worker_reports"`
	CheckedAt          time.Time            `json:"checked_at"`
}

// GetWorkerHealth retrieves health status for a specific worker
func (r *teamCLIRunner) GetWorkerHealth(ctx context.Context, workspace, teamName, workerName string, maxHeartbeatAge time.Duration) (*WorkerHealthReport, error) {
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

	var statusResp struct {
		Worker string `json:"worker"`
		Status *struct {
			State             string    `json:"state"`
			CurrentTaskID     string    `json:"current_task_id,omitempty"`
			Reason            string    `json:"reason,omitempty"`
			ConsecutiveErrors int       `json:"consecutive_errors"`
			UpdatedAt         time.Time `json:"updated_at"`
		} `json:"status"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-worker-status", map[string]string{
		"team_name": teamName,
		"worker":    workerName,
	}, &statusResp); err != nil {
		return nil, fmt.Errorf("read worker status: %w", err)
	}

	report := &WorkerHealthReport{
		WorkerName: workerName,
		Status:     "unknown",
	}

	if heartbeatResp.Heartbeat != nil {
		hb := heartbeatResp.Heartbeat
		report.IsAlive = hb.Alive
		report.TotalTurns = hb.TurnCount
		report.LastTurnAt = hb.LastTurnAt

		if !hb.LastTurnAt.IsZero() {
			age := time.Since(hb.LastTurnAt).Milliseconds()
			report.HeartbeatAge = &age

			if age > maxHeartbeatAge.Milliseconds() {
				report.IsAlive = false
			}
		}
	}

	if statusResp.Status != nil {
		report.Status = statusResp.Status.State
		report.CurrentTaskID = statusResp.Status.CurrentTaskID
		report.ConsecutiveErrors = statusResp.Status.ConsecutiveErrors
	}

	if !report.IsAlive {
		report.Status = "dead"
	}

	return report, nil
}

// GetTeamHealth retrieves health status for all workers in a team
func (r *teamCLIRunner) GetTeamHealth(ctx context.Context, workspace, teamName string, maxHeartbeatAge time.Duration) (*TeamHealthSummary, error) {
	snapshot, err := r.fetchSnapshot(ctx, workspace, teamName)
	if err != nil {
		return nil, fmt.Errorf("fetch team snapshot: %w", err)
	}

	summary := &TeamHealthSummary{
		TeamName:      teamName,
		TotalWorkers:  snapshot.Summary.WorkerCount,
		WorkerReports: make([]WorkerHealthReport, 0, snapshot.Summary.WorkerCount),
		CheckedAt:     time.Now(),
	}

	for _, worker := range snapshot.Summary.Workers {
		report, err := r.GetWorkerHealth(ctx, workspace, teamName, worker.Name, maxHeartbeatAge)
		if err != nil {
			r.logger.Warn("failed to get worker health",
				"team", teamName,
				"worker", worker.Name,
				"error", err,
			)
			continue
		}

		summary.WorkerReports = append(summary.WorkerReports, *report)

		switch report.Status {
		case "dead":
			summary.DeadWorkers++
		case "quarantined":
			summary.QuarantinedWorkers++
		case "idle", "working", "active":
			if report.IsAlive {
				summary.HealthyWorkers++
			} else {
				summary.DeadWorkers++
			}
		}
	}

	return summary, nil
}

// CheckWorkerNeedsIntervention checks if a worker requires intervention
func (r *teamCLIRunner) CheckWorkerNeedsIntervention(ctx context.Context, workspace, teamName, workerName string, maxHeartbeatAge time.Duration) (string, error) {
	report, err := r.GetWorkerHealth(ctx, workspace, teamName, workerName, maxHeartbeatAge)
	if err != nil {
		return "", err
	}

	if !report.IsAlive {
		ageStr := "unknown"
		if report.HeartbeatAge != nil {
			ageStr = fmt.Sprintf("%ds", *report.HeartbeatAge/1000)
		}
		return fmt.Sprintf("Worker is dead: heartbeat stale for %s", ageStr), nil
	}

	if report.Status == "quarantined" {
		return fmt.Sprintf("Worker self-quarantined after %d consecutive errors", report.ConsecutiveErrors), nil
	}

	if report.ConsecutiveErrors >= 2 {
		return fmt.Sprintf("Worker has %d consecutive errors — at risk of quarantine", report.ConsecutiveErrors), nil
	}

	return "", nil
}
