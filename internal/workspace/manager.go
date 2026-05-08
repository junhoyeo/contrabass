package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/junhoyeo/contrabass/internal/types"
)

type Manager struct {
	baseDir   string
	gitBinary string

	mu         sync.RWMutex
	active     map[string]string
	issueLocks sync.Map
}

func NewManager(baseDir string) *Manager {
	return &Manager{
		baseDir:   baseDir,
		gitBinary: "git",
		active:    make(map[string]string),
	}
}

func (m *Manager) Create(ctx context.Context, issue types.Issue) (string, error) {
	if issue.ID == "" {
		return "", errors.New("issue id is required")
	}

	unlock := m.lockIssue(issue.ID)
	defer unlock()

	workspacePath := m.workspacePath(issue.ID)

	m.mu.RLock()
	trackedPath, tracked := m.active[issue.ID]
	m.mu.RUnlock()
	if tracked {
		if info, err := os.Stat(trackedPath); err == nil && info.IsDir() {
			return trackedPath, nil
		}
	}

	// If the path already exists, only reuse it when it is a registered git
	// worktree. A bare directory left behind by a previous crashed run
	// (containing only `.omx/` or `.contrabass/` subdirs) must be torn down
	// before `git worktree add` can succeed; otherwise omx launches in what
	// looks like the parent repo's working tree and fails the
	// `leader_workspace_dirty` check.
	if info, err := os.Stat(workspacePath); err == nil && info.IsDir() {
		if m.isRegisteredWorktree(ctx, workspacePath) {
			m.mu.Lock()
			m.active[issue.ID] = workspacePath
			m.mu.Unlock()
			return workspacePath, nil
		}
		if err := os.RemoveAll(workspacePath); err != nil {
			return "", fmt.Errorf("clean stale workspace dir for issue %s: %w", issue.ID, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return "", fmt.Errorf("create workspace parent directory: %w", err)
	}

	// Choose the branch the agent will commit on. Issue.BranchName is the
	// canonical per-issue branch (e.g. "symphony/zii-65") that downstream
	// verify-success-with-diff and dashboard rendering both reference. Fall
	// back to issue.ID for trackers that don't populate BranchName.
	branchName := strings.TrimSpace(issue.BranchName)
	if branchName == "" {
		branchName = issue.ID
	}

	// Primary: create-and-checkout a brand-new branch.
	// Fallback 1: branch already exists (-B re-creates and points at HEAD).
	// Fallback 2: branch exists and we want to attach existing branch as-is.
	primaryOut, primaryErr := m.runGit(ctx, "worktree", "add", "-b", branchName, workspacePath)
	if primaryErr != nil {
		_, recreateErr := m.runGit(ctx, "worktree", "add", "-B", branchName, workspacePath)
		if recreateErr != nil {
			_, attachErr := m.runGit(ctx, "worktree", "add", workspacePath, branchName)
			if attachErr != nil {
				return "", fmt.Errorf(
					"create git worktree for issue %s on branch %s: primary -b failed: %v (output=%s); -B retry failed: %v; attach existing branch failed: %w",
					issue.ID, branchName, primaryErr, primaryOut, recreateErr, attachErr,
				)
			}
		}
	}

	// Verify the worktree actually registered before declaring success.
	if !m.isRegisteredWorktree(ctx, workspacePath) {
		return "", fmt.Errorf(
			"git worktree add for issue %s reported success but path %s is not a registered worktree",
			issue.ID, workspacePath,
		)
	}

	m.mu.Lock()
	m.active[issue.ID] = workspacePath
	m.mu.Unlock()

	return workspacePath, nil
}

func (m *Manager) Cleanup(ctx context.Context, issueID string) error {
	if issueID == "" {
		return nil
	}

	unlock := m.lockIssue(issueID)
	defer unlock()

	workspacePath := m.workspacePath(issueID)
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		m.mu.Lock()
		delete(m.active, issueID)
		m.mu.Unlock()
		m.issueLocks.Delete(issueID)
		return nil
	}

	output, err := m.runGit(ctx, "worktree", "remove", workspacePath, "--force")
	if err != nil {
		if !strings.Contains(output, "is not a working tree") {
			return fmt.Errorf("remove git worktree for issue %s: %w", issueID, err)
		}
	}

	m.mu.Lock()
	delete(m.active, issueID)
	m.mu.Unlock()
	m.issueLocks.Delete(issueID)

	return nil
}

// CleanupAll snapshots issue IDs tracked at the call start and cleans up only
// that snapshot. Any Create that starts after the snapshot may leave new active
// workspaces that require a later CleanupAll call.
func (m *Manager) CleanupAll(ctx context.Context) error {
	issueIDs := m.List()
	var errs []error

	for _, issueID := range issueIDs {
		if err := m.Cleanup(ctx, issueID); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (m *Manager) Exists(issueID string) bool {
	m.mu.RLock()
	workspacePath, ok := m.active[issueID]
	m.mu.RUnlock()
	if !ok {
		return false
	}

	info, err := os.Stat(workspacePath)
	return err == nil && info.IsDir()
}

func (m *Manager) List() []string {
	m.mu.RLock()
	issueIDs := make([]string, 0, len(m.active))
	for issueID := range m.active {
		issueIDs = append(issueIDs, issueID)
	}
	m.mu.RUnlock()

	sort.Strings(issueIDs)
	return issueIDs
}

func (m *Manager) workspacePath(issueID string) string {
	return filepath.Join(m.baseDir, "workspaces", issueID)
}

func (m *Manager) lockIssue(issueID string) func() {
	issueLock, _ := m.issueLocks.LoadOrStore(issueID, &sync.Mutex{})
	mu := issueLock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// isRegisteredWorktree returns true when path appears in `git worktree list`.
// Used to distinguish real worktrees from bare directories left by crashed
// runs so Create knows which paths are safe to reuse vs. tear down.
//
// macOS aliases `/tmp` → `/private/tmp` and `/var` → `/private/var`, so the
// path Go reports via `filepath.Abs` may differ from what git prints in
// `worktree list --porcelain`. Symlink-resolve both sides before comparing
// so test temp dirs and production paths match correctly.
func (m *Manager) isRegisteredWorktree(ctx context.Context, path string) bool {
	output, err := m.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	abs := resolvedAbs(path)
	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		entry := resolvedAbs(strings.TrimSpace(rest))
		if entry == abs {
			return true
		}
	}
	return false
}

// resolvedAbs returns the absolute, symlink-resolved form of path. Falls back
// to the un-resolved absolute path (or the input verbatim) on any error so
// callers always get a usable string.
func resolvedAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func (m *Manager) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.gitBinary, args...)
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return string(output), fmt.Errorf("git executable not found: %w", err)
	}

	return string(output), fmt.Errorf("git %s failed: %w; output: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
}
