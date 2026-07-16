package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/web"
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

	ctx := context.Background()
	issue, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:    "Forward headless team events",
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	originalStartTeamWebServer := startTeamWebServer
	originalRunRootTeamIssue := runRootTeamIssue
	t.Cleanup(func() {
		startTeamWebServer = originalStartTeamWebServer
		runRootTeamIssue = originalRunRootTeamIssue
	})

	webEvents := make(chan web.WebEvent, 1)
	called := false
	startTeamWebServer = func(_ context.Context, _ *log.Logger, port int) (chan<- web.WebEvent, error) {
		called = true
		assert.Equal(t, 43111, port)
		return webEvents, nil
	}
	runRootTeamIssue = func(opts teamRunOptions, hooks teamRunHooks) error {
		require.Equal(t, issue.ID, opts.IssueID)
		require.Equal(t, "team-alpha", opts.TeamName)
		for _, handler := range hooks.EventHandlers {
			handler(ctx, types.TeamEvent{
				Type:      "tool_call",
				TeamName:  opts.TeamName,
				Timestamp: issue.CreatedAt,
				Data: map[string]interface{}{
					"tool_name": "rg",
				},
			})
		}
		return localTracker.UpdateIssueState(ctx, opts.IssueID, types.Released)
	}

	err = runTeamExecutionApp(ctx, cfgPath, watcher, nil, true, true, 43111)
	require.NoError(t, err)
	assert.True(t, called)

	select {
	case event := <-webEvents:
		assert.Equal(t, web.WebEventTeam, event.Kind)
		assert.Equal(t, "tool_call", event.Type)
	default:
		t.Fatal("expected forwarded team event")
	}
}

func TestRunTeamExecutionWebServerExplainsMissingDashboardAssets(t *testing.T) {
	webEvents, err := runTeamExecutionWebServer(context.Background(), nil, 0)

	require.Error(t, err)
	assert.Nil(t, webEvents)
	assert.ErrorContains(t, err, "web dashboard assets are unavailable in this build")
	assert.ErrorContains(t, err, "dashboard_dist")
}

func TestPublishTeamWebEventGuaranteesCriticalEvents(t *testing.T) {
	ctx := context.Background()
	webEvents := make(chan web.WebEvent, 1)
	webEvents <- web.WebEvent{Type: "already-buffered"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		publishTeamWebEvent(ctx, webEvents, types.TeamEvent{
			Type:      "pipeline_completed",
			TeamName:  "team-alpha",
			Timestamp: time.Now().UTC(),
		})
	}()

	select {
	case <-done:
		t.Fatal("critical team event should wait until the sink has capacity")
	case <-time.After(20 * time.Millisecond):
	}

	assert.Equal(t, "already-buffered", (<-webEvents).Type)
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("critical team event was not forwarded after capacity freed")
	}
	assert.Equal(t, "pipeline_completed", (<-webEvents).Type)
}

func TestPublishTeamWebEventDropsNonCriticalEventsWhenSinkFull(t *testing.T) {
	ctx := context.Background()
	webEvents := make(chan web.WebEvent, 1)
	webEvents <- web.WebEvent{Type: "already-buffered"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		publishTeamWebEvent(ctx, webEvents, types.TeamEvent{
			Type:      "tool_call",
			TeamName:  "team-alpha",
			Timestamp: time.Now().UTC(),
		})
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("non-critical team event blocked on full sink")
	}
	assert.Equal(t, "already-buffered", (<-webEvents).Type)
	assert.Empty(t, webEvents)
}

func TestIsCriticalTeamWebEventIncludesPipelineLifecycle(t *testing.T) {
	assert.True(t, isCriticalTeamWebEvent("pipeline_started"))
	assert.True(t, isCriticalTeamWebEvent("pipeline_completed"))
	assert.False(t, isCriticalTeamWebEvent("tool_call"))
}

func TestValidateTeamExecutionConfigRejectsExternalTrackers(t *testing.T) {
	t.Parallel()

	err := validateTeamExecutionConfig(&config.WorkflowConfig{
		Tracker: config.TrackerConfig{Type: "github"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, `tracker.type internal/local`)
}
