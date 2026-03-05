package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

func TestManager_CreateConcurrentSameIssueSerialized(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueID := "ISSUE-SAME"
	workspacePath := filepath.Join(repoDir, "workspaces", issueID)

	const workers = 8
	start := make(chan struct{})
	errCh := make(chan error, workers)
	pathCh := make(chan string, workers)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			path, err := mgr.Create(ctx, types.Issue{ID: issueID})
			if err != nil {
				errCh <- err
				return
			}
			pathCh <- path
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)
	close(pathCh)

	for err := range errCh {
		require.NoError(t, err)
	}
	for path := range pathCh {
		assert.Equal(t, workspacePath, path)
	}
	assert.Equal(t, []string{issueID}, mgr.List())
}

func TestManager_CreateCleanupConcurrentSameIssueSerialized(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueID := "ISSUE-RACE"
	workspacePath := filepath.Join(repoDir, "workspaces", issueID)

	const iterations = 40
	for range iterations {
		start := make(chan struct{})
		errCh := make(chan error, 2)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			_, err := mgr.Create(ctx, types.Issue{ID: issueID})
			errCh <- err
		}()

		go func() {
			defer wg.Done()
			<-start
			errCh <- mgr.Cleanup(ctx, issueID)
		}()

		close(start)
		wg.Wait()
		close(errCh)

		for err := range errCh {
			require.NoError(t, err)
		}

		exists := mgr.Exists(issueID)
		assert.Equal(t, dirExists(workspacePath), exists)
	}

	require.NoError(t, mgr.Cleanup(ctx, issueID))
	assert.False(t, mgr.Exists(issueID))
	assert.NoDirExists(t, workspacePath)
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

func TestManager_CreateStaleTrackedEntry(t *testing.T) {
	t.Parallel()

	// Test that Create handles stale tracked entries (map entry exists but directory doesn't)
	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueID := "ISSUE-STALE"

	// Create a workspace
	firstPath, err := mgr.Create(ctx, types.Issue{ID: issueID})
	require.NoError(t, err)
	assert.DirExists(t, firstPath)

	// Manually delete the directory to simulate stale entry
	err = os.RemoveAll(firstPath)
	require.NoError(t, err)
	assert.NoDirExists(t, firstPath)

	// Also remove the worktree entry so Create can recreate it
	runGit(t, repoDir, "worktree", "remove", "--force", firstPath)

	// Create again - should detect stale entry and recreate
	secondPath, err := mgr.Create(ctx, types.Issue{ID: issueID})
	require.NoError(t, err)
	assert.Equal(t, firstPath, secondPath)
	assert.DirExists(t, secondPath)
	assert.True(t, mgr.Exists(issueID))
}

func TestManager_CleanupNotAWorkingTree(t *testing.T) {
	t.Parallel()

	// Test that Cleanup gracefully handles "is not a working tree" error
	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueID := "ISSUE-NOTREE"

	// Create a workspace
	path, err := mgr.Create(ctx, types.Issue{ID: issueID})
	require.NoError(t, err)
	assert.DirExists(t, path)

	// Manually remove the worktree directory to simulate "not a working tree" scenario
	err = os.RemoveAll(path)
	require.NoError(t, err)

	// Cleanup should not error even though git worktree remove will fail
	err = mgr.Cleanup(ctx, issueID)
	require.NoError(t, err)
	assert.False(t, mgr.Exists(issueID))
}

func TestManager_CreateContextCancelled(t *testing.T) {
	t.Parallel()

	// Test that Create respects context cancellation
	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := mgr.Create(ctx, types.Issue{ID: "ISSUE-CANCEL"})
	require.Error(t, err)
	// Should be a context error or git command error due to cancellation
	assert.True(t, errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "git"))
}

func TestManager_CleanupContinuesWhenBeforeRemoveHookTimesOut(t *testing.T) {
	t.Parallel()

	t.Run("workspace_and_config_test.exs", func(t *testing.T) {
		repoDir := initGitRepo(t)
		mgr := NewManager(repoDir)
		ctx := context.Background()

		gitPath, err := exec.LookPath("git")
		require.NoError(t, err)

		issueFail := "ISSUE-HOOK-TIMEOUT"
		issueOK := "ISSUE-OK"
		failWorkspace := filepath.Join(repoDir, "workspaces", issueFail)
		okWorkspace := filepath.Join(repoDir, "workspaces", issueOK)

		require.NoError(t, os.MkdirAll(failWorkspace, 0o755))
		require.NoError(t, os.MkdirAll(okWorkspace, 0o755))

		mgr.mu.Lock()
		mgr.active[issueFail] = failWorkspace
		mgr.active[issueOK] = okWorkspace
		mgr.mu.Unlock()

		fakeGitPath := filepath.Join(t.TempDir(), "fake-git.sh")
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"worktree\" ] && [ \"$2\" = \"remove\" ]; then\n" +
			"  case \"$3\" in\n" +
			"    *ISSUE-HOOK-TIMEOUT)\n" +
			"      echo \"before_remove hook timed out\" >&2\n" +
			"      exit 1\n" +
			"      ;;\n" +
			"    *)\n" +
			"      rm -rf \"$3\"\n" +
			"      exit 0\n" +
			"      ;;\n" +
			"  esac\n" +
			"fi\n" +
			"exec \"" + gitPath + "\" \"$@\"\n"
		require.NoError(t, os.WriteFile(fakeGitPath, []byte(script), 0o755))

		mgr.gitBinary = fakeGitPath

		err = mgr.CleanupAll(ctx)
		require.Error(t, err)
		assert.ErrorContains(t, err, "before_remove hook timed out")

		assert.True(t, mgr.Exists(issueFail), "failing cleanup should keep active workspace")
		assert.False(t, mgr.Exists(issueOK), "cleanup should continue to succeeding workspace")
	})
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
