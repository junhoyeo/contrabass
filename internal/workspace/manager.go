package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/junhoyeo/contrabass/internal/types"
)

// issueIDPattern restricts issue IDs to alphanumeric, hyphen, and underscore.
// This prevents path-traversal via "../" or other metacharacters.
var issueIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type Manager struct {
	baseDir   string
	gitBinary string

	mu           sync.RWMutex
	active       map[string]string
	issueLocksMu sync.Mutex
	issueLocks   map[string]*issueLockEntry
}

type issueLockEntry struct {
	mu   sync.Mutex
	refs int
}

func NewManager(baseDir string) *Manager {
	return &Manager{
		baseDir:    baseDir,
		gitBinary:  "git",
		active:     make(map[string]string),
		issueLocks: make(map[string]*issueLockEntry),
	}
}

func (m *Manager) Create(ctx context.Context, issue types.Issue) (string, error) {
	if issue.ID == "" {
		return "", errors.New("issue id is required")
	}
	if !issueIDPattern.MatchString(issue.ID) {
		return "", fmt.Errorf("issue id %q contains invalid characters", issue.ID)
	}

	unlock := m.lockIssue(issue.ID)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}

	workspacePath := m.workspacePath(issue.ID)

	m.mu.RLock()
	trackedPath, tracked := m.active[issue.ID]
	m.mu.RUnlock()
	if tracked {
		if info, err := os.Stat(trackedPath); err == nil && info.IsDir() {
			registered, err := m.isRegisteredWorktree(ctx, trackedPath)
			if err != nil {
				return "", fmt.Errorf("verify tracked workspace for issue %s: %w", issue.ID, err)
			}
			if registered {
				return trackedPath, nil
			}
		}
		m.mu.Lock()
		delete(m.active, issue.ID)
		m.mu.Unlock()
	}

	// If the path already exists, only reuse it when it is a registered git
	// worktree. A bare directory left behind by a previous crashed run
	// (containing only `.omx/` or `.contrabass/` subdirs) must be torn down
	// before `git worktree add` can succeed; otherwise omx launches in what
	// looks like the parent repo's working tree and fails the
	// `leader_workspace_dirty` check.
	if info, err := os.Stat(workspacePath); err == nil && info.IsDir() {
		registered, err := m.isRegisteredWorktree(ctx, workspacePath)
		if err != nil {
			return "", fmt.Errorf("verify existing workspace for issue %s: %w", issue.ID, err)
		}
		if registered {
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

	// If the base directory is not a git repo, create a plain directory
	// instead of a git worktree. This is needed for tests and ephemeral
	// environments that do not have a git repository. Only a definitive
	// "not a git repository" answer takes this path — any other rev-parse
	// failure (transient IO error, canceled context) used to silently create
	// a plain dir INSIDE a real repo, confusing worktree bookkeeping later.
	notRepo, gitErr := m.isOutsideGitRepo(ctx)
	if gitErr != nil {
		return "", gitErr
	}
	if notRepo {
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			return "", fmt.Errorf("create plain workspace dir for issue %s: %w", issue.ID, err)
		}
		m.mu.Lock()
		m.active[issue.ID] = workspacePath
		m.mu.Unlock()
		return workspacePath, nil
	}

	// Choose the branch the agent will commit on. Issue.BranchName is the
	// canonical per-issue branch (e.g. "symphony/zii-65") that downstream
	// verify-success-with-diff and dashboard rendering both reference. Fall
	// back to issue.ID for trackers that don't populate BranchName.
	branchName := strings.TrimSpace(issue.BranchName)
	if branchName == "" {
		branchName = issue.ID
	}

	if err := m.addWorktree(ctx, branchName, workspacePath); err != nil {
		// A worktree registration whose directory was deleted externally
		// (rm -rf without `worktree remove`) blocks every `worktree add` at
		// this path until pruned. Prune stale registrations once and retry
		// before giving up — otherwise the issue is permanently undispatchable.
		prunable, lookupErr := m.hasPrunableWorktreeRegistration(ctx, workspacePath)
		if lookupErr != nil {
			return "", fmt.Errorf("inspect stale worktree registration for issue %s: %w", issue.ID, lookupErr)
		}
		if !prunable {
			return "", fmt.Errorf("create git worktree for issue %s on branch %s: %w", issue.ID, branchName, err)
		}
		if _, pruneErr := m.runGit(ctx, "worktree", "prune"); pruneErr != nil {
			return "", fmt.Errorf(
				"create git worktree for issue %s on branch %s: %w (git worktree prune also failed: %v)",
				issue.ID, branchName, err, pruneErr,
			)
		}
		if retryErr := m.addWorktree(ctx, branchName, workspacePath); retryErr != nil {
			return "", fmt.Errorf(
				"create git worktree for issue %s on branch %s (after prune): %w",
				issue.ID, branchName, retryErr,
			)
		}
	}

	// Verify the worktree actually registered before declaring success.
	registered, err := m.isRegisteredWorktree(ctx, workspacePath)
	if err != nil {
		return "", fmt.Errorf("verify created workspace for issue %s: %w", issue.ID, err)
	}
	if !registered {
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

// addWorktree creates the worktree on a brand-new branch, falling back to
// attaching the existing branch as-is (retry after a partial run, resumed
// issue) so the agent's prior commits stay on the branch ref. Never falls
// back to `worktree add -B`: it re-creates the branch AT HEAD, silently
// discarding those commits and defeating branch-advance verification.
func (m *Manager) addWorktree(ctx context.Context, branchName, workspacePath string) error {
	primaryOut, primaryErr := m.runGit(ctx, "worktree", "add", "-b", branchName, workspacePath)
	if primaryErr == nil {
		return nil
	}
	_, attachErr := m.runGit(ctx, "worktree", "add", workspacePath, branchName)
	if attachErr != nil {
		return fmt.Errorf(
			"primary -b failed: %v (output=%s); attach existing branch failed: %w",
			primaryErr, primaryOut, attachErr,
		)
	}
	return nil
}

func (m *Manager) Cleanup(ctx context.Context, issueID string) error {
	if issueID == "" {
		return nil
	}
	// Mirror Create's validation: issueID is joined into a filesystem path
	// below and, on the plain-directory fallback, passed to os.RemoveAll.
	if !issueIDPattern.MatchString(issueID) {
		return fmt.Errorf("issue id %q contains invalid characters", issueID)
	}

	unlock := m.lockIssue(issueID)
	defer unlock()

	workspacePath := m.workspacePath(issueID)
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		m.mu.Lock()
		delete(m.active, issueID)
		m.mu.Unlock()
		return nil
	}

	output, err := m.runGit(ctx, "worktree", "remove", workspacePath, "--force")
	if err != nil {
		if !isNotAWorktreeError(output) {
			return fmt.Errorf("remove git worktree for issue %s: %w", issueID, err)
		}
		// Plain-directory workspace: created by the non-git fallback in
		// Create, or a stale dir inside a repo. `git worktree remove` can't
		// delete it, so remove it directly — otherwise these accumulate
		// forever (and Cleanup errors every time in non-git mode).
		if err := os.RemoveAll(workspacePath); err != nil {
			return fmt.Errorf("remove plain workspace dir for issue %s: %w", issueID, err)
		}
	}

	m.mu.Lock()
	delete(m.active, issueID)
	m.mu.Unlock()

	return nil
}

// isNotAWorktreeError matches the two `git worktree remove` failures that mean
// the path is a plain directory rather than a registered worktree: removing a
// non-worktree path inside a repo ("is not a working tree") and running
// outside any repo at all ("not a git repository", the non-git fallback mode).
func isNotAWorktreeError(output string) bool {
	return strings.Contains(output, "is not a working tree") ||
		strings.Contains(output, "not a git repository")
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

// lockIssue serializes Create/Cleanup per issue ID. References include both
// holders and waiters, so an entry is only reclaimed after the final caller
// releases it; this avoids the old delete/load race without retaining one
// mutex for every issue ever seen.
func (m *Manager) lockIssue(issueID string) func() {
	m.issueLocksMu.Lock()
	entry := m.issueLocks[issueID]
	if entry == nil {
		entry = &issueLockEntry{}
		m.issueLocks[issueID] = entry
	}
	entry.refs++
	m.issueLocksMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.issueLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && m.issueLocks[issueID] == entry {
			delete(m.issueLocks, issueID)
		}
		m.issueLocksMu.Unlock()
	}
}

// isRegisteredWorktree reports whether path is a non-prunable registered
// worktree with Git's linked-worktree metadata in place. Lookup failures are
// returned so Create never deletes a potentially valid workspace on uncertain
// Git state.
// Used to distinguish real worktrees from bare directories left by crashed
// runs so Create knows which paths are safe to reuse vs. tear down.
//
// macOS aliases `/tmp` → `/private/tmp` and `/var` → `/private/var`, so the
// path Go reports via `filepath.Abs` may differ from what git prints in
// `worktree list --porcelain`. Symlink-resolve both sides before comparing
// so test temp dirs and production paths match correctly.
func (m *Manager) isRegisteredWorktree(ctx context.Context, path string) (bool, error) {
	registered, prunable, err := m.worktreeRegistration(ctx, path)
	if err != nil {
		return false, err
	}
	if !registered || prunable {
		return false, nil
	}

	gitFile, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read worktree metadata: %w", err)
	}
	return strings.HasPrefix(strings.TrimSpace(string(gitFile)), "gitdir: "), nil
}

func (m *Manager) hasPrunableWorktreeRegistration(ctx context.Context, path string) (bool, error) {
	registered, prunable, err := m.worktreeRegistration(ctx, path)
	if err != nil || !registered {
		return false, err
	}
	if prunable {
		return true, nil
	}
	// Older Git versions do not always print a porcelain `prunable` line for
	// a removed linked worktree, even though `worktree add` rejects the stale
	// registration. The absent directory is equally definitive here.
	_, statErr := os.Stat(path)
	return errors.Is(statErr, os.ErrNotExist), nil
}

func (m *Manager) worktreeRegistration(ctx context.Context, path string) (bool, bool, error) {
	output, err := m.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return false, false, err
	}
	target := resolvedAbs(path)
	currentPath := ""
	currentPrunable := false
	matchCurrent := func() (bool, bool) {
		return currentPath != "" && resolvedAbs(currentPath) == target, currentPrunable
	}
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			if registered, prunable := matchCurrent(); registered {
				return true, prunable, nil
			}
			currentPath = strings.TrimSpace(rest)
			currentPrunable = false
			continue
		}
		if strings.HasPrefix(line, "prunable") {
			currentPrunable = true
		}
	}
	registered, prunable := matchCurrent()
	return registered, prunable, nil
}

// resolvedAbs returns the absolute, symlink-resolved form of path. When the
// final path does not exist (as with a stale worktree), it resolves the
// longest existing parent and appends the absent suffix. That preserves the
// canonical /private/var form Git emits on macOS for a missing /var path.
// Falls back to the un-resolved absolute path (or the input verbatim) on any
// error so callers always get a usable string.
func resolvedAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}

	var suffix []string
	for parent := abs; ; parent = filepath.Dir(parent) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...)
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
	}
	return abs
}

// isOutsideGitRepo reports whether baseDir is definitively outside any git
// repository. A missing git binary or any other rev-parse failure is returned
// as an error rather than treated as "outside" — failing closed here keeps a
// transient error inside a real repo from downgrading the workspace to a
// plain directory.
func (m *Manager) isOutsideGitRepo(ctx context.Context) (bool, error) {
	cmd := m.gitCommand(ctx, "rev-parse", "--git-dir")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return false, fmt.Errorf("git executable not found: %w", err)
	}
	if strings.Contains(string(output), "not a git repository") {
		return true, nil
	}
	return false, fmt.Errorf("git rev-parse --git-dir failed: %w; output: %s", err, strings.TrimSpace(string(output)))
}

func (m *Manager) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := m.gitCommand(ctx, args...)
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

func (m *Manager) gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, m.gitBinary, args...)
	cmd.Dir = m.baseDir
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "LC_ALL=") || strings.HasPrefix(entry, "LANG=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "LC_ALL=C", "LANG=C")
	return cmd
}
