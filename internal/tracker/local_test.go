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
