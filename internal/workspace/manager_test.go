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
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
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

// TestManager_CreateReattachesExistingBranchWithCommits pins the retry
// contract: when the per-issue branch already exists with commits from a
// previous run, Create must attach the worktree to it as-is. The old `-B`
// fallback re-created the branch at HEAD, silently discarding the agent's
// commits and defeating branch-advance verification.
func TestManager_CreateReattachesExistingBranchWithCommits(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issue := types.Issue{ID: "ISSUE-RETRY", BranchName: "symphony/issue-retry"}
	path, err := mgr.Create(ctx, issue)
	require.NoError(t, err)

	// Simulate agent work: a commit on the issue branch inside the worktree.
	workPath := filepath.Join(path, "work.txt")
	require.NoError(t, os.WriteFile(workPath, []byte("agent progress"), 0o644))
	runGit(t, path, "add", "work.txt")
	runGit(t, path, "commit", "-m", "agent progress")

	branchRevBefore := gitRevParse(t, path, "symphony/issue-retry")

	// Worktree torn down (crash cleanup, resumed issue) — the branch survives.
	require.NoError(t, mgr.Cleanup(ctx, issue.ID))
	assert.NoDirExists(t, path)

	secondPath, err := mgr.Create(ctx, issue)
	require.NoError(t, err)
	assert.Equal(t, path, secondPath)

	branchRevAfter := gitRevParse(t, secondPath, "symphony/issue-retry")
	assert.Equal(t, branchRevBefore, branchRevAfter,
		"re-creating the workspace must not move the issue branch ref")
	assert.FileExists(t, filepath.Join(secondPath, "work.txt"),
		"the agent's prior commit must be checked out in the re-attached worktree")
}

// TestManager_CleanupRemovesPlainDirOutsideGitRepo pins the non-git fallback
// contract: Cleanup must delete plain-directory workspaces instead of
// erroring on `git worktree remove` and leaving them to accumulate forever.
func TestManager_CleanupRemovesPlainDirOutsideGitRepo(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir() // not a git repository
	mgr := NewManager(baseDir)
	ctx := context.Background()

	path, err := mgr.Create(ctx, types.Issue{ID: "ISSUE-PLAIN"})
	require.NoError(t, err)
	assert.DirExists(t, path)

	require.NoError(t, mgr.Cleanup(ctx, "ISSUE-PLAIN"))
	assert.NoDirExists(t, path)
	assert.False(t, mgr.Exists("ISSUE-PLAIN"))
}

// TestManager_CleanupRemovesStalePlainDirInsideRepo covers the in-repo
// variant: a bare directory at the workspace path (left by a crashed run) is
// not a registered worktree, so `git worktree remove` refuses it — Cleanup
// must still delete it.
func TestManager_CleanupRemovesStalePlainDirInsideRepo(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	workspacePath := filepath.Join(repoDir, "workspaces", "ISSUE-BARE")
	require.NoError(t, os.MkdirAll(workspacePath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspacePath, "leftover.txt"), []byte("x"), 0o644))

	require.NoError(t, mgr.Cleanup(ctx, "ISSUE-BARE"))
	assert.NoDirExists(t, workspacePath)
}

// TestManager_CreateRecoversRegisteredButMissingWorktree pins the prune-retry
// path: when the workspace directory is deleted externally (rm -rf without
// `git worktree remove`), the stale registration blocks every `worktree add`
// at that path. Create must prune and retry instead of failing forever.
func TestManager_CreateRecoversRegisteredButMissingWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issue := types.Issue{ID: "ISSUE-GONE", BranchName: "symphony/issue-gone"}
	path, err := mgr.Create(ctx, issue)
	require.NoError(t, err)

	// Delete the directory without telling git — the registration remains.
	require.NoError(t, os.RemoveAll(path))

	secondPath, err := mgr.Create(ctx, issue)
	require.NoError(t, err, "Create must prune the stale registration and retry")
	assert.Equal(t, path, secondPath)
	assert.DirExists(t, secondPath)
}

func TestManager_CreateRebuildsRecreatedBareRegisteredWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	issue := types.Issue{ID: "ISSUE-RECREATED", BranchName: "symphony/issue-recreated"}
	path, err := NewManager(repoDir).Create(context.Background(), issue)
	require.NoError(t, err)

	// Leave Git's registration behind, then recreate only a plain directory at
	// the same path. A restarted manager must not accept it as a worktree.
	require.NoError(t, os.RemoveAll(path))
	require.NoError(t, os.MkdirAll(path, 0o755))
	staleFile := filepath.Join(path, "stale.txt")
	require.NoError(t, os.WriteFile(staleFile, []byte("not a worktree"), 0o644))

	rebuiltPath, err := NewManager(repoDir).Create(context.Background(), issue)
	require.NoError(t, err)
	assert.Equal(t, path, rebuiltPath)
	assert.NoFileExists(t, staleFile)
	gitFile, err := os.ReadFile(filepath.Join(rebuiltPath, ".git"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(string(gitFile)), "gitdir: "))
}

func TestManager_CreateDoesNotDeleteWorkspaceOnRegistrationLookupFailure(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	issue := types.Issue{ID: "ISSUE-LOOKUP", BranchName: "symphony/issue-lookup"}
	path, err := NewManager(repoDir).Create(context.Background(), issue)
	require.NoError(t, err)
	marker := filepath.Join(path, "uncommitted-work.txt")
	require.NoError(t, os.WriteFile(marker, []byte("preserve me"), 0o644))

	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	fakeGitPath := filepath.Join(t.TempDir(), "fail-worktree-list.sh")
	writeFakeGit(t, fakeGitPath, "#!/bin/sh\n"+
		"if [ \"$1\" = \"worktree\" ] && [ \"$2\" = \"list\" ]; then\n"+
		"  echo \"temporary git failure\" >&2\n"+
		"  exit 1\n"+
		"fi\n"+
		"exec \""+gitPath+"\" \"$@\"\n")

	resumed := NewManager(repoDir)
	resumed.gitBinary = fakeGitPath
	_, err = resumed.Create(context.Background(), issue)
	require.Error(t, err)
	assert.ErrorContains(t, err, "verify existing workspace")
	assert.FileExists(t, marker)
}

func TestManager_CreateCancelledDoesNotDeleteExistingWorkspace(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	issue := types.Issue{ID: "ISSUE-CANCELLED", BranchName: "symphony/issue-cancelled"}
	path, err := NewManager(repoDir).Create(context.Background(), issue)
	require.NoError(t, err)
	marker := filepath.Join(path, "uncommitted-work.txt")
	require.NoError(t, os.WriteFile(marker, []byte("preserve me"), 0o644))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewManager(repoDir).Create(ctx, issue)
	require.ErrorIs(t, err, context.Canceled)
	assert.FileExists(t, marker)
}

func TestManager_CreateDoesNotPruneForUnrelatedAddFailure(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	pruneMarker := filepath.Join(t.TempDir(), "pruned")
	fakeGitPath := filepath.Join(t.TempDir(), "fail-add.sh")
	writeFakeGit(t, fakeGitPath, "#!/bin/sh\n"+
		"if [ \"$1\" = \"worktree\" ] && [ \"$2\" = \"add\" ]; then\n"+
		"  echo \"synthetic add failure\" >&2\n"+
		"  exit 1\n"+
		"fi\n"+
		"if [ \"$1\" = \"worktree\" ] && [ \"$2\" = \"prune\" ]; then\n"+
		"  touch \""+pruneMarker+"\"\n"+
		"  exit 0\n"+
		"fi\n"+
		"exec \""+gitPath+"\" \"$@\"\n")

	mgr := NewManager(repoDir)
	mgr.gitBinary = fakeGitPath
	_, err = mgr.Create(context.Background(), types.Issue{ID: "ISSUE-ADD-FAIL"})
	require.Error(t, err)
	assert.NoFileExists(t, pruneMarker)
}

func TestManager_LockIssueReclaimsIdleEntry(t *testing.T) {
	t.Parallel()

	mgr := NewManager(t.TempDir())
	unlock := mgr.lockIssue("ISSUE-LOCK")
	mgr.issueLocksMu.Lock()
	assert.Len(t, mgr.issueLocks, 1)
	mgr.issueLocksMu.Unlock()

	unlock()
	mgr.issueLocksMu.Lock()
	assert.Empty(t, mgr.issueLocks)
	mgr.issueLocksMu.Unlock()
}

func TestManager_GitCommandsUseCLocale(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	fakeGitPath := filepath.Join(t.TempDir(), "locale-aware-git.sh")
	writeFakeGit(t, fakeGitPath, "#!/bin/sh\n"+
		"if [ \"$LC_ALL\" != \"C\" ] || [ \"$LANG\" != \"C\" ]; then\n"+
		"  echo \"git: no es un repositorio\" >&2\n"+
		"  exit 1\n"+
		"fi\n"+
		"echo \"fatal: not a git repository\" >&2\n"+
		"exit 128\n")

	mgr := NewManager(baseDir)
	mgr.gitBinary = fakeGitPath
	path, err := mgr.Create(context.Background(), types.Issue{ID: "ISSUE-LOCALE"})
	require.NoError(t, err)
	assert.DirExists(t, path)
}

func TestManager_CleanupRejectsInvalidIssueID(t *testing.T) {
	t.Parallel()

	mgr := NewManager(t.TempDir())

	err := mgr.Cleanup(context.Background(), "../escape")
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid characters")
}

func gitRevParse(t *testing.T, dir string, ref string) string {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git rev-parse %s failed: %s", ref, string(output))
	return strings.TrimSpace(string(output))
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
		writeFakeGit(t, fakeGitPath, script)

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

func TestManager_CreateRegistersWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issue := types.Issue{ID: "ISSUE-WT", BranchName: "symphony/issue-wt"}
	path, err := mgr.Create(ctx, issue)
	require.NoError(t, err)

	// `git worktree list --porcelain` SHALL contain the new path.
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git worktree list: %s", out)

	wantPath := resolvedAbs(path)
	var found bool
	for _, line := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if resolvedAbs(strings.TrimSpace(rest)) == wantPath {
			found = true
			break
		}
	}
	assert.Truef(t, found, "expected worktree %q in:\n%s", path, out)

	// .git is a worktree pointer file, not a directory.
	info, err := os.Stat(filepath.Join(path, ".git"))
	require.NoError(t, err, ".git pointer file should exist")
	assert.False(t, info.IsDir(),
		".git in a worktree must be a pointer file, not a directory")
}

func TestManager_CreateTearsDownStaleDir(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepo(t)
	mgr := NewManager(repoDir)
	ctx := context.Background()

	issueID := "ISSUE-STALE-DIR"
	workspacePath := filepath.Join(repoDir, "workspaces", issueID)
	require.NoError(t, os.MkdirAll(workspacePath, 0o755))
	stalePath := filepath.Join(workspacePath, "leftover.txt")
	require.NoError(t, os.WriteFile(stalePath, []byte("orphan"), 0o644))

	issue := types.Issue{ID: issueID, BranchName: "symphony/issue-stale-dir"}
	path, err := mgr.Create(ctx, issue)
	require.NoError(t, err)
	assert.Equal(t, workspacePath, path)

	_, err = os.Stat(stalePath)
	assert.Truef(t, errors.Is(err, os.ErrNotExist),
		"stale plain-dir contents must be torn down before recreate; stat err=%v", err)

	info, err := os.Stat(filepath.Join(path, ".git"))
	require.NoError(t, err, "post-recreate worktree must have a .git pointer file")
	assert.False(t, info.IsDir())
}

// writeFakeGit writes a shell script and ensures the fd is fully synced/closed
// before returning, preventing "text file busy" (ETXTBSY) on Linux when the
// script is executed immediately after creation.
func writeFakeGit(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())
	// Brief pause to allow the kernel to finish releasing the file after
	// close+sync — prevents sporadic ETXTBSY on Linux CI runners.
	time.Sleep(50 * time.Millisecond)
}
