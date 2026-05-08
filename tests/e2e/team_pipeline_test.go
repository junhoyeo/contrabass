package e2e

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/team"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

type teamEventCollector struct {
	mu     sync.Mutex
	events []types.TeamEvent
}

func collectTeamEvents(events <-chan types.TeamEvent) *teamEventCollector {
	c := &teamEventCollector{}
	go func() {
		for evt := range events {
			c.mu.Lock()
			c.events = append(c.events, evt)
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *teamEventCollector) CountType(eventType string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, e := range c.events {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

func (c *teamEventCollector) HasType(eventType string) bool {
	return c.CountType(eventType) > 0
}

func (c *teamEventCollector) All() []types.TeamEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.events)
}

func TestTeamPipelineFullCycle(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.WorkflowConfig{
		AgentTimeoutMsRaw: 30000,
		StallTimeoutMsRaw: 15000,
		ModelRaw:          "test",
		PromptTemplate:    "test",
		Tracker:           config.TrackerConfig{Type: "internal"},
		Team: config.TeamSectionConfig{
			MaxWorkers:  3,
			MaxFixLoops: 2,
			StateDir:    filepath.Join(tmpDir, "team-state"),
		},
	}

	runner := &agent.MockRunner{
		Events: []types.AgentEvent{
			{Type: "session/started"},
			{Type: "turn/completed"},
		},
		Delay: 10 * time.Millisecond,
	}

	ws := newTeamTestWorkspaceManager(t, tmpDir, "test-team", 3)
	coord := team.NewCoordinator("test-team", cfg, runner, ws, slog.Default())

	require.NoError(t, coord.Initialize(types.TeamConfig{MaxWorkers: 3, BoardIssueID: "TEST-1"}))

	tasks := []types.TeamTask{
		{ID: "001-test-plan", Subject: "plan", Description: "plan work", Status: types.TaskPending},
		{ID: "002-test-prd", Subject: "prd", Description: "prd work", Status: types.TaskPending},
		{ID: "003-test-exec", Subject: "exec", Description: "exec work", Status: types.TaskPending},
	}

	collector := collectTeamEvents(coord.Events)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- coord.Run(ctx, tasks)
	}()

	var runErr error
	require.Eventually(t, func() bool {
		select {
		case runErr = <-done:
			return true
		default:
			return false
		}
	}, 8*time.Second, 20*time.Millisecond)
	require.NoError(t, runErr)

	require.Eventually(t, func() bool {
		return collector.HasType("pipeline_started") && collector.HasType("pipeline_completed")
	}, 2*time.Second, 20*time.Millisecond)

	for _, taskID := range []string{"001-test-plan", "002-test-prd", "003-test-exec"} {
		assert.GreaterOrEqual(t, countTaskEvent(collector.All(), "task_claimed", taskID), 1)
		assert.GreaterOrEqual(t, countTaskEvent(collector.All(), "task_completed", taskID), 1)
	}

	for _, phase := range []string{string(types.PhasePlan), string(types.PhasePRD), string(types.PhaseExec), string(types.PhaseVerify)} {
		assert.True(t, hasPhaseStarted(collector.All(), phase), "expected phase_started for %s", phase)
	}

	assert.Equal(t, 0, collector.CountType("task_failed"))
}

func TestTeamPipelineContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.WorkflowConfig{
		AgentTimeoutMsRaw: 30000,
		StallTimeoutMsRaw: 15000,
		ModelRaw:          "test",
		PromptTemplate:    "test",
		Tracker:           config.TrackerConfig{Type: "internal"},
		Team: config.TeamSectionConfig{
			MaxWorkers:  3,
			MaxFixLoops: 2,
			StateDir:    filepath.Join(tmpDir, "team-state"),
		},
	}

	runner := &agent.MockRunner{
		Events: []types.AgentEvent{
			{Type: "session/started"},
			{Type: "turn/completed"},
		},
		Delay: 5 * time.Second,
	}

	ws := newTeamTestWorkspaceManager(t, tmpDir, "cancel-team", 3)
	coord := team.NewCoordinator("cancel-team", cfg, runner, ws, slog.Default())

	require.NoError(t, coord.Initialize(types.TeamConfig{MaxWorkers: 3, BoardIssueID: "TEST-1"}))

	tasks := []types.TeamTask{
		{ID: "001-test-plan", Subject: "plan", Description: "plan work", Status: types.TaskPending},
		{ID: "002-test-prd", Subject: "prd", Description: "prd work", Status: types.TaskPending},
		{ID: "003-test-exec", Subject: "exec", Description: "exec work", Status: types.TaskPending},
	}

	collector := collectTeamEvents(coord.Events)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- coord.Run(ctx, tasks)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("expected coordinator to stop after context cancellation")
	}

	assert.False(t, collector.HasType("pipeline_completed"))
}

func TestTeamPipelineAgentFailure(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.WorkflowConfig{
		AgentTimeoutMsRaw: 30000,
		StallTimeoutMsRaw: 15000,
		ModelRaw:          "test",
		PromptTemplate:    "test",
		Tracker:           config.TrackerConfig{Type: "internal"},
		Team: config.TeamSectionConfig{
			MaxWorkers:  3,
			MaxFixLoops: 2,
			StateDir:    filepath.Join(tmpDir, "team-state"),
		},
	}

	runner := &agent.MockRunner{
		Events:  []types.AgentEvent{{Type: "session/started"}},
		Delay:   10 * time.Millisecond,
		DoneErr: errors.New("agent crashed"),
	}

	ws := newTeamTestWorkspaceManager(t, tmpDir, "failure-team", 3)
	coord := team.NewCoordinator("failure-team", cfg, runner, ws, slog.Default())

	require.NoError(t, coord.Initialize(types.TeamConfig{MaxWorkers: 3, BoardIssueID: "TEST-1"}))

	tasks := []types.TeamTask{
		{ID: "001-test-plan", Subject: "plan", Description: "plan work", Status: types.TaskPending},
		{ID: "002-test-prd", Subject: "prd", Description: "prd work", Status: types.TaskPending},
		{ID: "003-test-exec", Subject: "exec", Description: "exec work", Status: types.TaskPending},
	}

	collector := collectTeamEvents(coord.Events)

	err := coord.Run(context.Background(), tasks)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent process failed")

	require.Eventually(t, func() bool {
		return collector.CountType("task_failed") > 0
	}, 2*time.Second, 20*time.Millisecond)
}

func newTeamTestWorkspaceManager(t *testing.T, baseDir string, teamName string, maxWorkers int) *workspace.Manager {
	t.Helper()

	// workspace.Manager.Create now provisions real git worktrees, so baseDir
	// must be a git repository with at least one commit.
	initE2EGitRepo(t, baseDir)

	return workspace.NewManager(baseDir)
}

func initE2EGitRepo(t *testing.T, dir string) {
	t.Helper()

	runE2EGit(t, dir, "init")
	runE2EGit(t, dir, "config", "user.email", "test@example.com")
	runE2EGit(t, dir, "config", "user.name", "test")

	readme := filepath.Join(dir, "README.md")
	require.NoError(t, os.WriteFile(readme, []byte("# e2e\n"), 0o644))

	runE2EGit(t, dir, "add", "README.md")
	runE2EGit(t, dir, "commit", "-m", "initial commit")
}

func runE2EGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(output))
}

func countTaskEvent(events []types.TeamEvent, eventType string, taskID string) int {
	count := 0
	for _, e := range events {
		if e.Type != eventType {
			continue
		}
		id, _ := e.Data["task_id"].(string)
		if id == taskID {
			count++
		}
	}
	return count
}

func hasPhaseStarted(events []types.TeamEvent, phase string) bool {
	for _, e := range events {
		if e.Type != "phase_started" {
			continue
		}
		p, _ := e.Data["phase"].(string)
		if p == phase {
			return true
		}
	}
	return false
}
