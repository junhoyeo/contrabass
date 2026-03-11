package team

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgentRunner struct {
	delay time.Duration

	mu         sync.Mutex
	startCalls []mockStartCall
	stopCalls  int
	closeCalls int

	active    int32
	maxActive int32
}

type mockStartCall struct {
	IssueID string
	TaskID  string
	WorkDir string
	Prompt  string
}

func (m *mockAgentRunner) Start(ctx context.Context, issue types.Issue, workDir, prompt string) (*agent.AgentProcess, error) {
	active := atomic.AddInt32(&m.active, 1)
	for {
		max := atomic.LoadInt32(&m.maxActive)
		if active <= max {
			break
		}
		if atomic.CompareAndSwapInt32(&m.maxActive, max, active) {
			break
		}
	}

	m.mu.Lock()
	m.startCalls = append(m.startCalls, mockStartCall{
		IssueID: issue.ID,
		TaskID:  issue.Identifier,
		WorkDir: workDir,
		Prompt:  prompt,
	})
	m.mu.Unlock()

	events := make(chan types.AgentEvent)
	done := make(chan error, 1)

	go func() {
		defer atomic.AddInt32(&m.active, -1)
		defer close(events)

		if m.delay > 0 {
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-time.After(m.delay):
			}
		}

		done <- nil
	}()

	return &agent.AgentProcess{
		PID:    12345,
		Events: events,
		Done:   done,
	}, nil
}

func (m *mockAgentRunner) Stop(*agent.AgentProcess) error {
	m.mu.Lock()
	m.stopCalls++
	m.mu.Unlock()
	return nil
}

func (m *mockAgentRunner) Close() error {
	m.mu.Lock()
	m.closeCalls++
	m.mu.Unlock()
	return nil
}

func (m *mockAgentRunner) snapshot() (starts []mockStartCall, maxActive int32, stopCalls int, closeCalls int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	starts = make([]mockStartCall, len(m.startCalls))
	copy(starts, m.startCalls)
	return starts, atomic.LoadInt32(&m.maxActive), m.stopCalls, m.closeCalls
}

func TestCoordinatorGoroutineModeRegression_RunPipeline(t *testing.T) {
	tests := []struct {
		name       string
		maxWorkers int
		execTasks  int
	}{
		{
			name:       "two workers process exec tasks and complete pipeline",
			maxWorkers: 2,
			execTasks:  4,
		},
		{
			name:       "three workers process exec tasks and complete pipeline",
			maxWorkers: 3,
			execTasks:  5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			stateDir := filepath.Join(baseDir, "state")
			teamName := "goroutine-regression"

			runner := &mockAgentRunner{delay: 40 * time.Millisecond}
			cfg := &config.WorkflowConfig{
				Agent: config.AgentConfig{Type: "codex"},
				Team: config.TeamSectionConfig{
					MaxWorkers:        tc.maxWorkers,
					MaxFixLoops:       2,
					ClaimLeaseSeconds: 60,
					StateDir:          stateDir,
				},
			}

			workspaceMgr := workspace.NewManager(baseDir)
			require.NoError(t, prepareGoroutineWorkspaces(baseDir, teamName, tc.maxWorkers))

			coordinator := NewCoordinator(
				teamName,
				cfg,
				runner,
				workspaceMgr,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			require.NoError(t, coordinator.Initialize(types.TeamConfig{
				MaxWorkers:        cfg.TeamMaxWorkers(),
				MaxFixLoops:       cfg.TeamMaxFixLoops(),
				ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
				StateDir:          cfg.TeamStateDir(),
				AgentType:         cfg.AgentType(),
			}))

			tasks := buildGoroutineRegressionTasks(tc.execTasks)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			require.NoError(t, coordinator.Run(ctx, tasks))

			phase, err := coordinator.phases.CurrentPhase(teamName)
			require.NoError(t, err)
			assert.Equal(t, types.PhaseComplete, phase)

			history, err := coordinator.phases.TransitionHistory(teamName)
			require.NoError(t, err)
			require.Len(t, history, 4)
			assert.Equal(t, types.PhasePRD, history[0].To)
			assert.Equal(t, types.PhaseExec, history[1].To)
			assert.Equal(t, types.PhaseVerify, history[2].To)
			assert.Equal(t, types.PhaseComplete, history[3].To)

			storedTasks, err := coordinator.tasks.ListTasks(teamName)
			require.NoError(t, err)
			require.Len(t, storedTasks, len(tasks))

			execCompletedByWorker := 0
			for _, task := range storedTasks {
				assert.Equal(t, types.TaskCompleted, task.Status)
				if strings.HasPrefix(task.ID, "03-") {
					if strings.Contains(task.Result, "completed by worker-") {
						execCompletedByWorker++
					}
				}
			}
			assert.Equal(t, tc.execTasks, execCompletedByWorker)

			startCalls, maxActive, stopCalls, closeCalls := runner.snapshot()
			assert.Len(t, startCalls, len(tasks))
			assert.GreaterOrEqual(t, maxActive, int32(2), "expected goroutine workers to execute concurrently")
			assert.GreaterOrEqual(t, stopCalls, 1)
			assert.LessOrEqual(t, stopCalls, len(tasks))
			assert.Equal(t, 0, closeCalls)

			events := collectTeamEvents(coordinator.Events)
			assertEventCount(t, events, "team_created", 1)
			assertEventCount(t, events, "pipeline_started", 1)
			assertEventCount(t, events, "phase_started", 4)
			assertEventCount(t, events, "task_claimed", len(tasks))
			assertEventCount(t, events, "task_completed", len(tasks))
			assertEventCount(t, events, "pipeline_completed", 1)
			assertEventCount(t, events, "task_failed", 0)

			last := events[len(events)-1]
			assert.Equal(t, "pipeline_completed", last.Type)
			assert.Equal(t, string(types.PhaseComplete), last.Data["phase"])
		})
	}
}

func TestConfigWorkerModeDefaultsToGoroutine(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.WorkflowConfig
		want string
	}{
		{
			name: "nil config defaults to goroutine",
			cfg:  nil,
			want: "goroutine",
		},
		{
			name: "empty worker mode defaults to goroutine",
			cfg:  &config.WorkflowConfig{},
			want: "goroutine",
		},
		{
			name: "unknown worker mode falls back to goroutine",
			cfg: &config.WorkflowConfig{Team: config.TeamSectionConfig{
				WorkerMode: "invalid-mode",
			}},
			want: "goroutine",
		},
		{
			name: "tmux mode remains tmux",
			cfg: &config.WorkflowConfig{Team: config.TeamSectionConfig{
				WorkerMode: "tmux",
			}},
			want: "tmux",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg == nil {
				assert.Equal(t, tc.want, (*config.WorkflowConfig)(nil).WorkerMode())
				return
			}
			assert.Equal(t, tc.want, tc.cfg.WorkerMode())
		})
	}
}

func buildGoroutineRegressionTasks(execTasks int) []types.TeamTask {
	tasks := []types.TeamTask{
		{ID: "01-plan", Subject: "Planning", Description: "Plan the work"},
		{ID: "02-prd", Subject: "PRD", Description: "Write requirements"},
	}

	for i := 0; i < execTasks; i++ {
		tasks = append(tasks, types.TeamTask{
			ID:          fmt.Sprintf("03-exec-%02d", i+1),
			Subject:     fmt.Sprintf("Exec %d", i+1),
			Description: "Execute implementation task",
		})
	}

	return tasks
}

func prepareGoroutineWorkspaces(baseDir, teamName string, maxWorkers int) error {
	workers := []string{"coordinator", "verifier"}
	for i := range maxWorkers {
		workers = append(workers, fmt.Sprintf("worker-%d", i))
	}

	for _, workerID := range workers {
		issueID := fmt.Sprintf("team-%s-%s", teamName, workerID)
		path := filepath.Join(baseDir, "workspaces", issueID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}

	return nil
}

func collectTeamEvents(eventsCh <-chan types.TeamEvent) []types.TeamEvent {
	events := make([]types.TeamEvent, 0)
	for event := range eventsCh {
		events = append(events, event)
	}
	return events
}

func assertEventCount(t *testing.T, events []types.TeamEvent, eventType string, expected int) {
	t.Helper()
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	assert.Equal(t, expected, count, "event type %s", eventType)
}
