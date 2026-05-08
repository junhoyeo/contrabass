package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceHeadSHA_FreshRepo(t *testing.T) {
	repo := initGitRepo(t)
	want := gitOutput(t, repo, "rev-parse", "HEAD")

	got, err := workspaceHeadSHA(context.Background(), repo)

	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Len(t, got, 40)
}

func TestWorkspaceHeadSHA_BadPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	_, err := workspaceHeadSHA(context.Background(), missing)

	require.Error(t, err)
}

func TestVerifyBranchAdvanced_HeadMatches(t *testing.T) {
	repo := initGitRepo(t)
	claimHead := gitOutput(t, repo, "rev-parse", "HEAD")

	advanced, reason, err := verifyBranchAdvanced(context.Background(), repo, "HEAD", claimHead)

	require.NoError(t, err)
	assert.False(t, advanced)
	assert.Equal(t, "branch_unchanged", reason)
}

func TestVerifyBranchAdvanced_HeadDiffers(t *testing.T) {
	repo := initGitRepo(t)
	claimHead := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "advance")

	advanced, reason, err := verifyBranchAdvanced(context.Background(), repo, "HEAD", claimHead)

	require.NoError(t, err)
	assert.True(t, advanced)
	assert.Empty(t, reason)
}

func TestVerifyBranchAdvanced_EmptyClaimHead(t *testing.T) {
	repo := initGitRepo(t)

	advanced, reason, err := verifyBranchAdvanced(context.Background(), repo, "HEAD", "")

	require.NoError(t, err)
	assert.True(t, advanced)
	assert.Equal(t, "no_claim_head", reason)
}

func TestVerifyBranchAdvanced_GitError(t *testing.T) {
	nonRepo := t.TempDir()

	advanced, reason, err := verifyBranchAdvanced(context.Background(), nonRepo, "HEAD", "abc123")

	require.Error(t, err)
	assert.True(t, advanced)
	assert.Equal(t, "git_error", reason)
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	gitRun(t, repo, "init")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "file.txt"), []byte("initial\n"), 0o644))
	gitRun(t, repo, "add", "file.txt")
	gitRun(t, repo, "commit", "-m", "initial")
	return repo
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()

	baseArgs := []string{"-c", "user.name=Contrabass Test", "-c", "user.email=contrabass@example.test"}
	cmd := exec.Command("git", append(baseArgs, args...)...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}
