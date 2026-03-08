package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestRunTeamExecutionLoopDispatchesBoardIssuesThroughTeams(t *testing.T) {
	boardDir := filepath.Join(t.TempDir(), "board")
	cfgPath := writeRootWorkflowConfig(t, fmt.Sprintf(`---
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/internal
tracker:
  type: internal
  board_dir: %q
---
Prompt.
`, boardDir))

	watcher, err := config.NewWatcher(cfgPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = watcher.Stop()
	})

	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: "CB",
		Actor:       "test-bot",
	})
	ctx := context.Background()
	_, err = localTracker.InitBoard(ctx)
	require.NoError(t, err)

	issue, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:    "Ship default team execution",
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	originalRunRootTeamIssue := runRootTeamIssue
	t.Cleanup(func() {
		runRootTeamIssue = originalRunRootTeamIssue
	})

	runRootTeamIssue = func(opts teamRunOptions, hooks teamRunHooks) error {
		require.Equal(t, issue.ID, opts.IssueID)
		require.Equal(t, "team-alpha", opts.TeamName)
		for _, handler := range hooks.EventHandlers {
			handler(ctx, types.TeamEvent{
				Type:      "team_created",
				TeamName:  opts.TeamName,
				Timestamp: issue.CreatedAt,
				Data: map[string]interface{}{
					"max_workers":    2,
					"board_issue_id": issue.ID,
				},
			})
		}
		return localTracker.UpdateIssueState(ctx, opts.IssueID, types.Released)
	}

	events := make(chan types.TeamEvent, 4)
	require.NoError(t, runTeamExecutionLoop(ctx, cfgPath, watcher, events, true))

	dispatchedIssue, err := localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, tracker.LocalBoardStateDone, dispatchedIssue.State)

	select {
	case event := <-events:
		assert.Equal(t, "team_created", event.Type)
		assert.Equal(t, issue.ID, event.Data["board_issue_id"])
	default:
		t.Fatal("expected forwarded team event")
	}
}

func TestTeamExecutionAppPort(t *testing.T) {
	boardDir := filepath.Join(t.TempDir(), "board")
	cfgPath := writeRootWorkflowConfig(t, fmt.Sprintf(`---
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/internal
tracker:
  type: internal
  board_dir: %q
---
Prompt.
`, boardDir))

	watcher, err := config.NewWatcher(cfgPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = watcher.Stop()
	})

	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: "CB",
		Actor:       "test-bot",
	})
	_, err = localTracker.InitBoard(context.Background())
	require.NoError(t, err)

	originalStartTeamWebServer := startTeamWebServer
	t.Cleanup(func() {
		startTeamWebServer = originalStartTeamWebServer
	})

	called := false
	startTeamWebServer = func(_ context.Context, _ *log.Logger, port int) error {
		called = true
		assert.Equal(t, 43111, port)
		return nil
	}

	err = runTeamExecutionApp(context.Background(), cfgPath, watcher, nil, true, true, 43111)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestValidateTeamExecutionConfigRejectsExternalTrackers(t *testing.T) {
	t.Parallel()

	err := validateTeamExecutionConfig(&config.WorkflowConfig{
		Tracker: config.TrackerConfig{Type: "github"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, `tracker.type internal/local`)
}
