package tracker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestParseLocalBoardState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    LocalBoardState
		wantErr bool
	}{
		{name: "todo", input: "todo", want: LocalBoardStateTodo},
		{name: "in progress", input: "in_progress", want: LocalBoardStateInProgress},
		{name: "retry", input: "retry", want: LocalBoardStateRetry},
		{name: "done", input: "done", want: LocalBoardStateDone},
		{name: "invalid", input: "blocked", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLocalBoardState(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLocalTrackerLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := NewLocalTracker(LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "OPS",
		Actor:       "bot",
	})

	manifest, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)
	assert.Equal(t, "OPS", manifest.IssuePrefix)
	assert.Equal(t, 1, manifest.NextIssueNumber)

	issue, err := localTracker.CreateIssue(ctx, "Ship local tracker", "Implement the local board", []string{"tracker", "local"})
	require.NoError(t, err)
	assert.Equal(t, "OPS-1", issue.ID)
	assert.Equal(t, LocalBoardStateTodo, issue.State)

	fetched, err := localTracker.FetchIssues(ctx)
	require.NoError(t, err)
	require.Len(t, fetched, 1)
	assert.Equal(t, types.Unclaimed, fetched[0].State)
	assert.Equal(t, "local://OPS-1", fetched[0].URL)

	require.NoError(t, localTracker.ClaimIssue(ctx, issue.ID))
	current, err := localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, LocalBoardStateInProgress, current.State)
	assert.Equal(t, "bot", current.ClaimedBy)

	require.NoError(t, localTracker.UpdateIssueState(ctx, issue.ID, types.RetryQueued))
	current, err = localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, LocalBoardStateRetry, current.State)
	assert.Empty(t, current.ClaimedBy)

	require.NoError(t, localTracker.UpdateIssueState(ctx, issue.ID, types.Running))
	current, err = localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, LocalBoardStateInProgress, current.State)
	assert.Equal(t, "bot", current.ClaimedBy)

	require.NoError(t, localTracker.ReleaseIssue(ctx, issue.ID))
	current, err = localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, LocalBoardStateInProgress, current.State)
	assert.Empty(t, current.ClaimedBy)

	require.NoError(t, localTracker.PostComment(ctx, issue.ID, "Looks good"))
	comments, err := localTracker.ListComments(ctx, issue.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Equal(t, "bot", comments[0].Author)
	assert.Equal(t, "Looks good", comments[0].Body)

	require.NoError(t, localTracker.UpdateIssueState(ctx, issue.ID, types.Released))
	fetched, err = localTracker.FetchIssues(ctx)
	require.NoError(t, err)
	assert.Empty(t, fetched)

	allIssues, err := localTracker.ListIssues(ctx, true)
	require.NoError(t, err)
	require.Len(t, allIssues, 1)
	assert.Equal(t, LocalBoardStateDone, allIssues[0].State)
}

func TestLocalTrackerUpdateIssue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := NewLocalTracker(LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "OPS",
		Actor:       "bot",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	issue, err := localTracker.CreateIssue(ctx, "Ship board sync", "Wire team events back to the board", []string{"team"})
	require.NoError(t, err)

	updated, err := localTracker.UpdateIssue(ctx, issue.ID, func(issue *LocalBoardIssue) error {
		if issue.TrackerMeta == nil {
			issue.TrackerMeta = map[string]interface{}{}
		}
		issue.ClaimedBy = "team:issue-ops-1"
		issue.TrackerMeta["team_name"] = "issue-ops-1"
		issue.TrackerMeta["team_phase"] = "team-exec"
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, "team:issue-ops-1", updated.ClaimedBy)
	assert.Equal(t, "issue-ops-1", updated.TrackerMeta["team_name"])
	assert.Equal(t, "team-exec", updated.TrackerMeta["team_phase"])

	reloaded, err := localTracker.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, "team:issue-ops-1", reloaded.ClaimedBy)
	assert.Equal(t, "issue-ops-1", reloaded.TrackerMeta["team_name"])
	assert.Equal(t, "team-exec", reloaded.TrackerMeta["team_phase"])
}

func TestLocalTrackerCreateChildIssueAndAssign(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := NewLocalTracker(LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "CB",
		Actor:       "bot",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	parent, err := localTracker.CreateIssue(ctx, "Parent issue", "Top-level work item", []string{"epic"})
	require.NoError(t, err)

	child, err := localTracker.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:       "Child issue",
		Description: "Implement the first slice",
		ParentID:    parent.ID,
		Assignee:    "team-alpha",
		Labels:      []string{"slice"},
		BlockedBy:   []string{"CB-999"},
		TrackerMeta: map[string]interface{}{"kind": "implementation"},
	})
	require.NoError(t, err)

	assert.Equal(t, parent.ID, child.ParentID)
	assert.Equal(t, "team-alpha", child.Assignee)
	assert.Equal(t, []string{"CB-999"}, child.BlockedBy)
	assert.Equal(t, "implementation", child.TrackerMeta["kind"])

	parent, err = localTracker.GetIssue(ctx, parent.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{child.ID}, parent.ChildIDs)

	children, err := localTracker.ListChildIssues(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, child.ID, children[0].ID)

	assigned, err := localTracker.AssignIssue(ctx, child.ID, "team-beta")
	require.NoError(t, err)
	assert.Equal(t, "team-beta", assigned.Assignee)
}

func TestLocalTrackerFindDispatchableIssue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := NewLocalTracker(LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "CB",
		Actor:       "bot",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	blocker, err := localTracker.CreateIssue(ctx, "Blocker", "Must finish first", nil)
	require.NoError(t, err)
	blockedIssue, err := localTracker.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:     "Blocked issue",
		ParentID:  blocker.ID,
		BlockedBy: []string{blocker.ID},
	})
	require.NoError(t, err)

	ready, err := localTracker.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:    "Ready issue",
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	otherTeam, err := localTracker.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:    "Other team issue",
		Assignee: "team-beta",
	})
	require.NoError(t, err)

	selected, found, err := localTracker.FindDispatchableIssue(ctx, "team-alpha")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, blocker.ID, selected.ID)

	require.NoError(t, localTracker.UpdateIssueState(ctx, blocker.ID, types.Released))
	selected, found, err = localTracker.FindDispatchableIssue(ctx, "")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, blockedIssue.ID, selected.ID)

	require.NoError(t, localTracker.ClaimIssue(ctx, blockedIssue.ID))
	_, err = localTracker.AssignIssue(ctx, ready.ID, "team-gamma")
	require.NoError(t, err)
	selected, found, err = localTracker.FindDispatchableIssue(ctx, "team-beta")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, otherTeam.ID, selected.ID)
}

func TestLocalTrackerFindDispatchableIssueSkipsChildrenOfClaimedParent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	localTracker := NewLocalTracker(LocalConfig{
		BoardDir:    filepath.Join(t.TempDir(), "board"),
		IssuePrefix: "CB",
		Actor:       "bot",
	})

	_, err := localTracker.InitBoard(ctx)
	require.NoError(t, err)

	parent, err := localTracker.CreateIssue(ctx, "Epic", "Parent issue", nil)
	require.NoError(t, err)
	child, err := localTracker.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:    "Child issue",
		ParentID: parent.ID,
		Assignee: "team-alpha",
	})
	require.NoError(t, err)

	require.NoError(t, localTracker.ClaimIssue(ctx, parent.ID))

	_, found, err := localTracker.FindDispatchableIssue(ctx, "team-alpha")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, localTracker.ReleaseIssue(ctx, parent.ID))
	_, found, err = localTracker.FindDispatchableIssue(ctx, "team-alpha")
	require.NoError(t, err)
	assert.False(t, found)

	_, err = localTracker.MoveIssue(ctx, parent.ID, LocalBoardStateDone)
	require.NoError(t, err)

	selected, found, err := localTracker.FindDispatchableIssue(ctx, "team-alpha")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, child.ID, selected.ID)
}
