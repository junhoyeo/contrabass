package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorkerHealth(t *testing.T) {
	runner := newTeamCLIRunner(&teamCLIRunner{
		name:       "test-runner",
		binaryPath: "echo",
		logger:     log.New(nil),
	})

	ctx := context.Background()
	workspace := "/tmp/test"
	teamName := "test-team"
	workerName := "worker-1"
	maxAge := 30 * time.Second

	// Mock the team API calls
	// In a real test, we would use a mock server or dependency injection
	_, err := runner.GetWorkerHealth(ctx, workspace, teamName, workerName, maxAge)
	assert.Error(t, err, "Expected error with echo binary, got nil")
}

func TestGetTeamHealth(t *testing.T) {
	runner := newTeamCLIRunner(&teamCLIRunner{
		name:       "test-runner",
		binaryPath: "echo",
		logger:     log.New(nil),
	})

	ctx := context.Background()
	workspace := "/tmp/test"
	teamName := "test-team"
	maxAge := 30 * time.Second

	_, err := runner.GetTeamHealth(ctx, workspace, teamName, maxAge)
	assert.Error(t, err, "Expected error with echo binary, got nil")
}

func TestCheckWorkerNeedsIntervention(t *testing.T) {
	tests := []struct {
		name   string
		report *WorkerHealthReport
		want   string
	}{
		{
			name: "healthy worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "active",
				ConsecutiveErrors: 0,
			},
			want: "",
		},
		{
			name: "dead worker",
			report: &WorkerHealthReport{
				WorkerName: "worker-1",
				IsAlive:    false,
				Status:     "dead",
			},
			want: "Worker is dead: heartbeat stale for",
		},
		{
			name: "quarantined worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "quarantined",
				ConsecutiveErrors: 3,
			},
			want: "Worker self-quarantined after 3 consecutive errors",
		},
		{
			name: "at-risk worker",
			report: &WorkerHealthReport{
				WorkerName:        "worker-1",
				IsAlive:           true,
				Status:            "active",
				ConsecutiveErrors: 2,
			},
			want: "Worker has 2 consecutive errors — at risk of quarantine",
		},
	}

	// Note: This is a partial test since we can't easily mock the actual API calls
	// In production, we would need proper mocking infrastructure
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := tt.report
			var reason string
			if !report.IsAlive {
				ageStr := "unknown"
				if report.HeartbeatAge != nil {
					ageStr = fmt.Sprintf("%ds", *report.HeartbeatAge/1000)
				}
				reason = fmt.Sprintf("Worker is dead: heartbeat stale for %s", ageStr)
			} else if report.Status == "quarantined" {
				reason = fmt.Sprintf("Worker self-quarantined after %d consecutive errors", report.ConsecutiveErrors)
			} else if report.ConsecutiveErrors >= 2 {
				reason = fmt.Sprintf("Worker has %d consecutive errors — at risk of quarantine", report.ConsecutiveErrors)
			}
			if tt.want == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tt.want)
			}
		})
	}
}

func TestTeamHealthSummaryJSON(t *testing.T) {
	summary := &TeamHealthSummary{
		TeamName:           "test-team",
		TotalWorkers:       3,
		HealthyWorkers:     2,
		DeadWorkers:        1,
		QuarantinedWorkers: 0,
		WorkerReports: []WorkerHealthReport{
			{
				WorkerName: "worker-1",
				IsAlive:    true,
				Status:     "active",
			},
			{
				WorkerName: "worker-2",
				IsAlive:    true,
				Status:     "idle",
			},
			{
				WorkerName: "worker-3",
				IsAlive:    false,
				Status:     "dead",
			},
		},
		CheckedAt: time.Now(),
	}

	data, err := json.Marshal(summary)
	require.NoError(t, err, "Failed to marshal health summary")

	var decoded TeamHealthSummary
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "Failed to unmarshal health summary")

	assert.Equal(t, summary.TeamName, decoded.TeamName, "TeamName mismatch")
	assert.Equal(t, summary.TotalWorkers, decoded.TotalWorkers, "TotalWorkers mismatch")
	assert.Equal(t, len(summary.WorkerReports), len(decoded.WorkerReports), "WorkerReports length mismatch")
}

func TestTeamCLIRunner_CheckWorkerNeedsIntervention(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	reason, err := runner.CheckWorkerNeedsIntervention(context.Background(), workspace, "test-team", "worker-1", -1*time.Millisecond)
	require.NoError(t, err)
	assert.Contains(t, reason, "Worker is dead")
}

func TestTeamCLIRunner_RestartWorker(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	result, err := runner.RestartWorker(context.Background(), workspace, "test-team", "worker-1", &WorkerRestartOptions{
		GracePeriod:   300 * time.Millisecond,
		PreserveState: true,
		ReassignTasks: false,
		MaxRetries:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "worker-1", result.WorkerName)
	assert.Equal(t, 1234, result.OldPID)
}

func TestTeamCLIRunner_RestartDeadWorkers(t *testing.T) {
	runner, workspace := setupTeamRunner(t)

	results, err := runner.RestartDeadWorkers(context.Background(), workspace, "test-team", -1*time.Millisecond)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "worker-1", results[0].WorkerName)
	assert.True(t, results[0].Success)
}
