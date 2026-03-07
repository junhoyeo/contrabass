package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestBoardCommandLifecycle(t *testing.T) {
	t.Parallel()

	boardDir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()

		cmd := newRootCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetArgs(args)
		require.NoError(t, cmd.Execute())
		return buf.String()
	}

	initOutput := run("board", "init", "--dir", boardDir, "--prefix", "OPS")
	assert.Contains(t, initOutput, "initialized board")
	assert.Contains(t, initOutput, "OPS")

	createOutput := run(
		"board", "create",
		"--dir", boardDir,
		"--title", "Ship local tracker",
		"--description", "Implement the first local board slice",
		"--labels", "tracker,local",
	)

	issueID := strings.TrimSpace(createOutput)
	require.Equal(t, "OPS-1", issueID)

	listOutput := run("board", "list", "--dir", boardDir)
	assert.Contains(t, listOutput, "OPS-1")
	assert.Contains(t, listOutput, "todo")
	assert.Contains(t, listOutput, "Ship local tracker")

	moveOutput := run("board", "move", "--dir", boardDir, issueID, "in_progress")
	assert.Contains(t, moveOutput, "OPS-1 -> in_progress")

	commentOutput := run("board", "comment", "--dir", boardDir, issueID, "--body", "Looks good")
	assert.Contains(t, commentOutput, "commented on OPS-1")

	showOutput := run("board", "show", "--dir", boardDir, issueID)
	assert.Contains(t, showOutput, "ID: OPS-1")
	assert.Contains(t, showOutput, "State: in_progress")
	assert.Contains(t, showOutput, "Looks good")
}

func TestBoardDispatchUntilEmptyDrainsRunnableIssues(t *testing.T) {
	boardDir := filepath.Join(t.TempDir(), "board")
	cfgPath := writeBoardWorkflowConfig(t, boardDir)

	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: "CB",
		Actor:       "test-bot",
	})

	ctx := context.Background()
	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	parent, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:    "Parent issue",
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	child, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:     "Child issue",
		ParentID:  parent.ID,
		Assignee:  "team-alpha",
		BlockedBy: []string{parent.ID},
	})
	require.NoError(t, err)

	var calls []teamRunOptions
	err = dispatchBoardIssues(
		ctx,
		new(bytes.Buffer),
		localTracker,
		boardDispatchOptions{
			ConfigPath: cfgPath,
			UntilEmpty: true,
		},
		func(opts teamRunOptions) error {
			calls = append(calls, opts)
			return localTracker.UpdateIssueState(ctx, opts.IssueID, types.Released)
		},
	)
	require.NoError(t, err)

	require.Len(t, calls, 2)
	assert.Equal(t, []string{parent.ID, child.ID}, []string{calls[0].IssueID, calls[1].IssueID})
	assert.Equal(t, []string{"team-alpha", "team-alpha"}, []string{calls[0].TeamName, calls[1].TeamName})

	parent, err = localTracker.GetIssue(ctx, parent.ID)
	require.NoError(t, err)
	assert.Equal(t, tracker.LocalBoardStateDone, parent.State)

	child, err = localTracker.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	assert.Equal(t, tracker.LocalBoardStateDone, child.State)
}

func TestBoardDispatchUntilEmptyCommandReportsDrainSummary(t *testing.T) {
	boardDir := filepath.Join(t.TempDir(), "board")
	cfgPath := writeBoardWorkflowConfig(t, boardDir)

	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: "CB",
		Actor:       "test-bot",
	})
	ctx := context.Background()
	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	issueOne, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:    "First issue",
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	issueTwo, err := localTracker.CreateIssueWithOptions(ctx, tracker.LocalIssueCreateOptions{
		Title:    "Second issue",
		Assignee: "team-beta",
	})
	require.NoError(t, err)

	originalRunTeam := runBoardDispatchTeam
	runBoardDispatchTeam = func(opts teamRunOptions) error {
		return localTracker.UpdateIssueState(ctx, opts.IssueID, types.Released)
	}
	defer func() {
		runBoardDispatchTeam = originalRunTeam
	}()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"board", "dispatch",
		"--config", cfgPath,
		"--dir", boardDir,
		"--until-empty",
	})

	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, fmt.Sprintf("dispatched %s to team-alpha", issueOne.ID))
	assert.Contains(t, output, fmt.Sprintf("dispatched %s to team-beta", issueTwo.ID))
	assert.Contains(t, output, "drained board after 2 dispatches")
}

func TestBoardDispatchUntilEmptyCommandSucceedsWhenBoardIsAlreadyDrained(t *testing.T) {
	boardDir := filepath.Join(t.TempDir(), "board")
	cfgPath := writeBoardWorkflowConfig(t, boardDir)

	localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: "CB",
		Actor:       "test-bot",
	})
	_, err := localTracker.InitBoard(context.Background())
	require.NoError(t, err)

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"board", "dispatch",
		"--config", cfgPath,
		"--dir", boardDir,
		"--until-empty",
	})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "board already drained")
}

func writeBoardWorkflowConfig(t *testing.T, boardDir string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	content := fmt.Sprintf(`---
tracker:
  type: internal
  board_dir: %q
---
Internal board test workflow.
`, boardDir)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
