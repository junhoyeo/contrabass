package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_CreateAndCleanupLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		issueID string
	}{
		{name: "simple issue id", issueID: "ISSUE-101"},
		{name: "issue id with underscore", issueID: "ISSUE_202"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repoDir := initGitRepo(t)
			mgr := NewManager(repoDir)
			ctx := context.Background()

			path, err := mgr.Create(ctx, types.Issue{ID: tt.issueID})
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(repoDir, "workspaces", tt.issueID), path)
			assert.DirExists(t, path)
			assert.True(t, mgr.Exists(tt.issueID))
			assert.Equal(t, []string{tt.issueID}, mgr.List())

			err = mgr.Cleanup(ctx, tt.issueID)
			require.NoError(t, err)
			assert.False(t, mgr.Exists(tt.issueID))
			assert.Empty(t, mgr.List())
			assert.NoDirExists(t, path)
		})
	}
}

func TestManager_CreateReusesExistingWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issue := types.Issue{ID: "ISSUE-REUSE"}
	firstPath, err := mgr.Create(ctx, issue)
	require.NoError(t, err)

	markerPath := filepath.Join(firstPath, "marker.txt")
	err = os.WriteFile(markerPath, []byte("keep-me"), 0o644)
	require.NoError(t, err)

	secondPath, err := mgr.Create(ctx, issue)
	require.NoError(t, err)
	assert.Equal(t, firstPath, secondPath)
	assert.FileExists(t, markerPath)
	assert.Equal(t, []string{issue.ID}, mgr.List())
}

func TestManager_CleanupAllRemovesActiveWorktrees(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issues := []types.Issue{{ID: "ISSUE-A"}, {ID: "ISSUE-B"}, {ID: "ISSUE-C"}}
	for _, issue := range issues {
		_, err := mgr.Create(ctx, issue)
		require.NoError(t, err)
	}

	before := mgr.List()
	slices.Sort(before)
	assert.Equal(t, []string{"ISSUE-A", "ISSUE-B", "ISSUE-C"}, before)

	err := mgr.CleanupAll(ctx)
	require.NoError(t, err)
	assert.Empty(t, mgr.List())
	for _, issue := range issues {
		assert.False(t, mgr.Exists(issue.ID))
		assert.NoDirExists(t, filepath.Join(repoDir, "workspaces", issue.ID))
	}
}

func TestManager_CleanupAllBestEffortOnError(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	_, err := mgr.Create(ctx, types.Issue{ID: "ISSUE-OK"})
	require.NoError(t, err)
	_, err = mgr.Create(ctx, types.Issue{ID: "ISSUE-MISSING"})
	require.NoError(t, err)

	err = os.RemoveAll(filepath.Join(repoDir, "workspaces", "ISSUE-MISSING"))
	require.NoError(t, err)

	err = mgr.CleanupAll(ctx)
	require.NoError(t, err)
	assert.Empty(t, mgr.List())
	assert.False(t, mgr.Exists("ISSUE-OK"))
	assert.False(t, mgr.Exists("ISSUE-MISSING"))
}

func TestManager_CreateConcurrentIssueWorktrees(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueIDs := []string{"ISSUE-1", "ISSUE-2", "ISSUE-3", "ISSUE-4"}
	var wg sync.WaitGroup
	for _, issueID := range issueIDs {
		issueID := issueID
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.Create(ctx, types.Issue{ID: issueID})
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	got := mgr.List()
	slices.Sort(got)
	assert.Equal(t, issueIDs, got)
	for _, issueID := range issueIDs {
		assert.True(t, mgr.Exists(issueID))
		assert.DirExists(t, filepath.Join(repoDir, "workspaces", issueID))
	}
}

func TestManager_CreateReturnsClearErrorWhenGitUnavailable(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	mgr.gitBinary = "git-binary-that-does-not-exist"

	_, err := mgr.Create(context.Background(), types.Issue{ID: "ISSUE-NOGIT"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "git executable not found")
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "test")

	readmePath := filepath.Join(repoDir, "README.md")
	err := os.WriteFile(readmePath, []byte("# workspace test\n"), 0o644)
	require.NoError(t, err)

	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial commit")

	return repoDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(output))
}
