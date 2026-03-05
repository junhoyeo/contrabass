package workspace

import (
	"context"
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockManager_CreateRejectsEmptyIssueID(t *testing.T) {
	t.Parallel()

	mgr := NewMockManager(t.TempDir())
	_, err := mgr.Create(context.Background(), types.Issue{ID: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issue ID is required")
}

func TestMockManager_CreateAcceptsNonEmptyIssueID(t *testing.T) {
	t.Parallel()

	mgr := NewMockManager(t.TempDir())
	path, err := mgr.Create(context.Background(), types.Issue{ID: "ISSUE-1"})
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.True(t, mgr.Exists("ISSUE-1"))
}

func TestMockManager_CleanupRejectsEmptyIssueID(t *testing.T) {
	t.Parallel()

	mgr := NewMockManager(t.TempDir())
	err := mgr.Cleanup(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issue ID is required")
}

func TestMockManager_CleanupAcceptsNonEmptyIssueID(t *testing.T) {
	t.Parallel()

	mgr := NewMockManager(t.TempDir())
	_, err := mgr.Create(context.Background(), types.Issue{ID: "ISSUE-2"})
	require.NoError(t, err)
	assert.True(t, mgr.Exists("ISSUE-2"))

	err = mgr.Cleanup(context.Background(), "ISSUE-2")
	require.NoError(t, err)
	assert.False(t, mgr.Exists("ISSUE-2"))
}
