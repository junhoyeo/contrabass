package tracker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const (
	defaultLocalBoardDir    = ".contrabass/board"
	defaultLocalIssuePrefix = "CB"

	localBoardSchemaVersion = "1"
	defaultLocalActor       = "contrabass"
)

var localBoardPrefixPattern = regexp.MustCompile(`[^A-Za-z0-9]+`)
var localBoardTeamPattern = regexp.MustCompile(`[^a-z0-9]+`)

// localIssueIDPattern restricts issue IDs to filename-safe characters.
// Issue IDs arrive from the CLI and the web API and are joined into
// filesystem paths (issues/<id>.json, comments/<id>.jsonl); this prevents
// path traversal via "../" or other metacharacters.
var localIssueIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type LocalConfig struct {
	BoardDir    string
	IssuePrefix string
	Actor       string
}

type LocalBoardManifest struct {
	SchemaVersion   string    `json:"schema_version"`
	IssuePrefix     string    `json:"issue_prefix"`
	NextIssueNumber int       `json:"next_issue_number"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type LocalBoardState string

const (
	LocalBoardStateTodo       LocalBoardState = "todo"
	LocalBoardStateInProgress LocalBoardState = "in_progress"
	LocalBoardStateRetry      LocalBoardState = "retry"
	LocalBoardStateDone       LocalBoardState = "done"
)

type LocalBoardIssue struct {
	ID          string                 `json:"id"`
	Identifier  string                 `json:"identifier"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	State       LocalBoardState        `json:"state"`
	ParentID    string                 `json:"parent_id,omitempty"`
	ChildIDs    []string               `json:"child_ids,omitempty"`
	Assignee    string                 `json:"assignee,omitempty"`
	Labels      []string               `json:"labels,omitempty"`
	URL         string                 `json:"url,omitempty"`
	BranchName  string                 `json:"branch_name,omitempty"`
	BlockedBy   []string               `json:"blocked_by,omitempty"`
	ClaimedBy   string                 `json:"claimed_by,omitempty"`
	TrackerMeta map[string]interface{} `json:"tracker_meta,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

type LocalBoardComment struct {
	Author    string    `json:"author,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type LocalIssueCreateOptions struct {
	Title       string
	Description string
	ParentID    string
	Assignee    string
	Labels      []string
	BlockedBy   []string
	TrackerMeta map[string]interface{}
}

type LocalTracker struct {
	boardDir         string
	issuePrefix      string
	actor            string
	mu               sync.Mutex
	activeClaimLocks map[string]*fileLock
}

var _ Tracker = (*LocalTracker)(nil)

func NewLocalTracker(cfg LocalConfig) *LocalTracker {
	boardDir := cfg.BoardDir
	if strings.TrimSpace(boardDir) == "" {
		boardDir = defaultLocalBoardDir
	}

	actor := strings.TrimSpace(cfg.Actor)
	if actor == "" {
		actor = strings.TrimSpace(os.Getenv("USER"))
	}
	if actor == "" {
		actor = defaultLocalActor
	}

	return &LocalTracker{
		boardDir:         filepath.Clean(boardDir),
		issuePrefix:      sanitizeLocalIssuePrefix(cfg.IssuePrefix),
		actor:            actor,
		activeClaimLocks: make(map[string]*fileLock),
	}
}

func (t *LocalTracker) BoardDir() string {
	return t.boardDir
}

// lockBoardFile takes the cross-process board lock. The daemon and the
// `contrabass board`/`team` CLIs open the same BoardDir from separate
// processes; t.mu only serializes within one process. Callers must already
// hold t.mu (so lock ordering is always mu → file lock) and must call the
// returned release func.
func (t *LocalTracker) lockBoardFile() (func(), error) {
	lock := newFileLock(filepath.Join(t.boardDir, "board"))
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("lock board: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// tryLockIssueClaim takes a process-lifetime lock for one actively claimed
// issue. Unlike the board mutation lock, this lock remains held while the
// agent runs, so another daemon cannot mistake the live claim for an orphan.
// flock is released by the OS if the claiming process crashes.
func (t *LocalTracker) tryLockIssueClaim(issueID string) (*fileLock, error) {
	lock := newFileLock(filepath.Join(t.boardDir, "claims", issueID))
	acquired, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock claim for issue %s: %w", issueID, err)
	}
	if !acquired {
		return nil, fmt.Errorf("local board issue %q is already claimed by another process", issueID)
	}
	return lock, nil
}

func (t *LocalTracker) releaseIssueClaimLocked(issueID string) error {
	lock, ok := t.activeClaimLocks[issueID]
	if !ok {
		return nil
	}
	delete(t.activeClaimLocks, issueID)
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("unlock claim for issue %s: %w", issueID, err)
	}
	return nil
}

func ParseLocalBoardState(raw string) (LocalBoardState, error) {
	switch LocalBoardState(strings.TrimSpace(raw)) {
	case LocalBoardStateTodo:
		return LocalBoardStateTodo, nil
	case LocalBoardStateInProgress:
		return LocalBoardStateInProgress, nil
	case LocalBoardStateRetry:
		return LocalBoardStateRetry, nil
	case LocalBoardStateDone:
		return LocalBoardStateDone, nil
	default:
		return "", fmt.Errorf("unknown local board state: %q (supported: todo, in_progress, retry, done)", raw)
	}
}

// IssueState maps a board state to the orchestrator's claim state.
// in_progress maps to Claimed (not Running) so that a crashed run — which
// leaves the issue in_progress with no live agent — is picked up by
// orphan-claim recovery instead of being stranded forever. This mirrors the
// Linear adapter's started→Claimed mapping; the orchestrator itself decides
// what is actively Running from its in-memory managed set.
func (s LocalBoardState) IssueState() types.IssueState {
	switch s {
	case LocalBoardStateInProgress:
		return types.Claimed
	case LocalBoardStateRetry:
		return types.RetryQueued
	case LocalBoardStateDone:
		return types.Released
	default:
		return types.Unclaimed
	}
}

func (t *LocalTracker) InitBoard(ctx context.Context) (*LocalBoardManifest, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return nil, err
	}
	defer release()

	return t.ensureBoardLocked()
}

func (t *LocalTracker) FetchIssues(ctx context.Context) ([]types.Issue, error) {
	issues, err := t.ListIssues(ctx, false)
	if err != nil {
		return nil, err
	}

	normalized := make([]types.Issue, 0, len(issues))
	for _, issue := range issues {
		normalized = append(normalized, issue.toIssue())
	}

	return normalized, nil
}

func (t *LocalTracker) ClaimIssue(ctx context.Context, issueID string) error {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return err
	}
	if !localIssueIDPattern.MatchString(issueID) {
		return fmt.Errorf("local board issue id %q contains invalid characters", issueID)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return err
	}
	if _, claimed := t.activeClaimLocks[issueID]; claimed {
		return fmt.Errorf("local board issue %q is already claimed by this process", issueID)
	}

	claimLock, err := t.tryLockIssueClaim(issueID)
	if err != nil {
		return err
	}
	releaseClaimLock := true
	defer func() {
		if releaseClaimLock {
			_ = claimLock.Unlock()
		}
	}()

	issue, err := t.loadIssueLocked(issueID)
	if err != nil {
		return err
	}
	if issue.State == LocalBoardStateDone {
		return fmt.Errorf("local board issue %q is already done", issueID)
	}

	issue.State = LocalBoardStateInProgress
	issue.ClaimedBy = t.actor
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return err
	}
	t.activeClaimLocks[issueID] = claimLock
	releaseClaimLock = false
	return nil
}

func (t *LocalTracker) ReleaseIssue(ctx context.Context, issueID string) error {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return err
	}

	issue, err := t.loadIssueLocked(issueID)
	if err != nil {
		return err
	}

	issue.ClaimedBy = ""
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return err
	}
	return t.releaseIssueClaimLocked(issueID)
}

func (t *LocalTracker) UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return err
	}

	issue, err := t.loadIssueLocked(issueID)
	if err != nil {
		return err
	}

	issue.State = localBoardStateFromIssueState(state)
	switch issue.State {
	case LocalBoardStateInProgress:
		if issue.ClaimedBy == "" {
			issue.ClaimedBy = t.actor
		}
	default:
		issue.ClaimedBy = ""
	}
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return err
	}
	if issue.State != LocalBoardStateInProgress {
		return t.releaseIssueClaimLocked(issueID)
	}
	return nil
}

func (t *LocalTracker) PostComment(ctx context.Context, issueID string, body string) error {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return err
	}
	if _, err := t.loadIssueLocked(issueID); err != nil {
		return err
	}

	comment := LocalBoardComment{
		Author:    t.actor,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}

	return appendJSONLine(t.commentsPath(issueID), comment)
}

func (t *LocalTracker) AddComment(ctx context.Context, issueID string, body string) error {
	return t.PostComment(ctx, issueID, body)
}

func (t *LocalTracker) CreateIssue(ctx context.Context, title, description string, labels []string) (LocalBoardIssue, error) {
	return t.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:       title,
		Description: description,
		Labels:      labels,
	})
}

func (t *LocalTracker) CreateIssueWithOptions(ctx context.Context, opts LocalIssueCreateOptions) (LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return LocalBoardIssue{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return LocalBoardIssue{}, err
	}
	defer release()

	manifest, err := t.ensureBoardLocked()
	if err != nil {
		return LocalBoardIssue{}, err
	}

	var parent LocalBoardIssue
	if strings.TrimSpace(opts.ParentID) != "" {
		parent, err = t.loadIssueLocked(strings.TrimSpace(opts.ParentID))
		if err != nil {
			return LocalBoardIssue{}, err
		}
		opts.ParentID = parent.ID
	}

	issueID := fmt.Sprintf("%s-%d", manifest.IssuePrefix, manifest.NextIssueNumber)
	manifest.NextIssueNumber++
	manifest.UpdatedAt = time.Now().UTC()
	if err := writeJSONAtomic(t.manifestPath(), manifest); err != nil {
		return LocalBoardIssue{}, err
	}

	now := time.Now().UTC()
	issue := LocalBoardIssue{
		ID:          issueID,
		Identifier:  issueID,
		Title:       opts.Title,
		Description: opts.Description,
		State:       LocalBoardStateTodo,
		ParentID:    opts.ParentID,
		Assignee:    opts.Assignee,
		Labels:      slices.Clone(opts.Labels),
		URL:         fmt.Sprintf("local://%s", issueID),
		BlockedBy:   slices.Clone(opts.BlockedBy),
		TrackerMeta: maps.Clone(opts.TrackerMeta),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return LocalBoardIssue{}, err
	}

	if issue.ParentID != "" {
		if !slices.Contains(parent.ChildIDs, issue.ID) {
			parent.ChildIDs = append(parent.ChildIDs, issue.ID)
			parent.UpdatedAt = now
			if err := writeJSONAtomic(t.issuePath(parent.ID), parent); err != nil {
				return LocalBoardIssue{}, err
			}
		}
	}

	return issue, nil
}

func (t *LocalTracker) CreateChildIssue(
	ctx context.Context,
	parentID string,
	title string,
	description string,
	labels []string,
) (LocalBoardIssue, error) {
	return t.CreateIssueWithOptions(ctx, LocalIssueCreateOptions{
		Title:       title,
		Description: description,
		ParentID:    parentID,
		Labels:      labels,
	})
}

func (t *LocalTracker) ListIssues(ctx context.Context, includeDone bool) ([]LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := t.ensureBoardLocked(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(t.issuesDir())
	if err != nil {
		return nil, fmt.Errorf("reading issues directory: %w", err)
	}

	issues := make([]LocalBoardIssue, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var issue LocalBoardIssue
		if err := readJSONFile(filepath.Join(t.issuesDir(), entry.Name()), &issue); err != nil {
			return nil, err
		}
		if !includeDone && issue.State == LocalBoardStateDone {
			continue
		}
		issues = append(issues, issue)
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].CreatedAt.Equal(issues[j].CreatedAt) {
			return issues[i].ID < issues[j].ID
		}
		return issues[i].CreatedAt.Before(issues[j].CreatedAt)
	})

	return issues, nil
}

func (t *LocalTracker) GetIssue(ctx context.Context, issueID string) (LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return LocalBoardIssue{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := t.ensureBoardLocked(); err != nil {
		return LocalBoardIssue{}, err
	}

	return t.loadIssueLocked(issueID)
}

func (t *LocalTracker) AssignIssue(ctx context.Context, issueID string, assignee string) (LocalBoardIssue, error) {
	return t.UpdateIssue(ctx, issueID, func(issue *LocalBoardIssue) error {
		issue.Assignee = strings.TrimSpace(assignee)
		return nil
	})
}

func (t *LocalTracker) ListChildIssues(ctx context.Context, parentID string) ([]LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := t.ensureBoardLocked(); err != nil {
		return nil, err
	}

	parent, err := t.loadIssueLocked(parentID)
	if err != nil {
		return nil, err
	}

	children := make([]LocalBoardIssue, 0, len(parent.ChildIDs))
	for _, childID := range parent.ChildIDs {
		child, err := t.loadIssueLocked(childID)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}

	return children, nil
}

func (t *LocalTracker) FindDispatchableIssue(ctx context.Context, teamName string) (LocalBoardIssue, bool, error) {
	issues, err := t.ListIssues(ctx, false)
	if err != nil {
		return LocalBoardIssue{}, false, err
	}

	normalizedTeam := strings.TrimSpace(teamName)
	for _, issue := range issues {
		if issue.ClaimedBy != "" {
			continue
		}
		if issue.ParentID != "" {
			parent, err := t.GetIssue(ctx, issue.ParentID)
			if err != nil {
				return LocalBoardIssue{}, false, err
			}
			if parent.State != LocalBoardStateDone {
				continue
			}
		}
		if issue.State != LocalBoardStateTodo && issue.State != LocalBoardStateRetry {
			continue
		}
		if normalizedTeam != "" && !issueMatchesTeam(issue, normalizedTeam) {
			continue
		}

		blocked := false
		for _, blockerID := range issue.BlockedBy {
			blocker, err := t.GetIssue(ctx, blockerID)
			if err != nil {
				return LocalBoardIssue{}, false, err
			}
			if blocker.State != LocalBoardStateDone {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}

		return issue, true, nil
	}

	return LocalBoardIssue{}, false, nil
}

// UpdateIssue applies a mutation to a board issue atomically and persists the result.
func (t *LocalTracker) UpdateIssue(
	ctx context.Context,
	issueID string,
	mutate func(*LocalBoardIssue) error,
) (LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return LocalBoardIssue{}, err
	}
	if mutate == nil {
		return LocalBoardIssue{}, errors.New("mutate callback is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return LocalBoardIssue{}, err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return LocalBoardIssue{}, err
	}

	issue, err := t.loadIssueLocked(issueID)
	if err != nil {
		return LocalBoardIssue{}, err
	}

	if err := mutate(&issue); err != nil {
		return LocalBoardIssue{}, err
	}
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return LocalBoardIssue{}, err
	}

	return issue, nil
}

func (t *LocalTracker) MoveIssue(ctx context.Context, issueID string, state LocalBoardState) (LocalBoardIssue, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return LocalBoardIssue{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	release, err := t.lockBoardFile()
	if err != nil {
		return LocalBoardIssue{}, err
	}
	defer release()

	if _, err := t.ensureBoardLocked(); err != nil {
		return LocalBoardIssue{}, err
	}

	issue, err := t.loadIssueLocked(issueID)
	if err != nil {
		return LocalBoardIssue{}, err
	}

	issue.State = state
	switch state {
	case LocalBoardStateInProgress:
		if issue.ClaimedBy == "" {
			issue.ClaimedBy = t.actor
		}
	default:
		issue.ClaimedBy = ""
	}
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return LocalBoardIssue{}, err
	}

	return issue, nil
}

func (t *LocalTracker) ListComments(ctx context.Context, issueID string) ([]LocalBoardComment, error) {
	if err := checkLocalTrackerContext(ctx); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := t.ensureBoardLocked(); err != nil {
		return nil, err
	}
	if _, err := t.loadIssueLocked(issueID); err != nil {
		return nil, err
	}

	path := t.commentsPath(issueID)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []LocalBoardComment{}, nil
		}
		return nil, fmt.Errorf("opening comments file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	comments := []LocalBoardComment{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var comment LocalBoardComment
		if err := json.Unmarshal([]byte(line), &comment); err != nil {
			return nil, fmt.Errorf("parsing comment for %s: %w", issueID, err)
		}
		comments = append(comments, comment)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading comments file: %w", err)
	}

	return comments, nil
}

func (t *LocalTracker) ensureBoardLocked() (*LocalBoardManifest, error) {
	if err := os.MkdirAll(t.issuesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("creating issues directory: %w", err)
	}
	if err := os.MkdirAll(t.commentsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("creating comments directory: %w", err)
	}

	path := t.manifestPath()
	var manifest LocalBoardManifest
	if err := readJSONFile(path, &manifest); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		now := time.Now().UTC()
		manifest = LocalBoardManifest{
			SchemaVersion:   localBoardSchemaVersion,
			IssuePrefix:     t.issuePrefix,
			NextIssueNumber: 1,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := writeJSONAtomic(path, manifest); err != nil {
			return nil, err
		}
		return &manifest, nil
	}

	if manifest.SchemaVersion == "" {
		manifest.SchemaVersion = localBoardSchemaVersion
	}
	if manifest.IssuePrefix == "" {
		manifest.IssuePrefix = t.issuePrefix
	}
	if manifest.NextIssueNumber <= 0 {
		manifest.NextIssueNumber = 1
	}

	return &manifest, nil
}

func (t *LocalTracker) loadIssueLocked(issueID string) (LocalBoardIssue, error) {
	if !localIssueIDPattern.MatchString(issueID) {
		return LocalBoardIssue{}, fmt.Errorf("local board issue id %q contains invalid characters", issueID)
	}

	var issue LocalBoardIssue
	if err := readJSONFile(t.issuePath(issueID), &issue); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LocalBoardIssue{}, fmt.Errorf("local board issue %q not found", issueID)
		}
		return LocalBoardIssue{}, err
	}
	return issue, nil
}

func (t *LocalTracker) manifestPath() string {
	return filepath.Join(t.boardDir, "manifest.json")
}

func (t *LocalTracker) issuesDir() string {
	return filepath.Join(t.boardDir, "issues")
}

func (t *LocalTracker) commentsDir() string {
	return filepath.Join(t.boardDir, "comments")
}

func (t *LocalTracker) issuePath(issueID string) string {
	return filepath.Join(t.issuesDir(), issueID+".json")
}

func (t *LocalTracker) commentsPath(issueID string) string {
	return filepath.Join(t.commentsDir(), issueID+".jsonl")
}

func (i LocalBoardIssue) toIssue() types.Issue {
	trackerMeta := map[string]interface{}{
		"source":      "local",
		"claimed_by":  i.ClaimedBy,
		"assignee":    i.Assignee,
		"parent_id":   i.ParentID,
		"child_ids":   slices.Clone(i.ChildIDs),
		"board_state": i.State,
	}
	if i.TrackerMeta != nil {
		for key, value := range i.TrackerMeta {
			trackerMeta[key] = value
		}
	}

	return types.Issue{
		ID:            i.ID,
		Identifier:    i.Identifier,
		Title:         i.Title,
		Description:   i.Description,
		State:         i.State.IssueState(),
		Labels:        slices.Clone(i.Labels),
		URL:           i.URL,
		BranchName:    i.BranchName,
		BlockedBy:     slices.Clone(i.BlockedBy),
		ModelOverride: ParseModelOverride(i.Description),
		CreatedAt:     i.CreatedAt,
		UpdatedAt:     i.UpdatedAt,
		TrackerMeta:   trackerMeta,
	}
}

func localBoardStateFromIssueState(state types.IssueState) LocalBoardState {
	switch state {
	case types.Claimed, types.Running:
		return LocalBoardStateInProgress
	case types.RetryQueued:
		return LocalBoardStateRetry
	case types.Released:
		return LocalBoardStateDone
	default:
		return LocalBoardStateTodo
	}
}

func sanitizeLocalIssuePrefix(prefix string) string {
	trimmed := strings.TrimSpace(strings.ToUpper(prefix))
	if trimmed == "" {
		return defaultLocalIssuePrefix
	}

	sanitized := localBoardPrefixPattern.ReplaceAllString(trimmed, "")
	if sanitized == "" {
		return defaultLocalIssuePrefix
	}

	return sanitized
}

func issueMatchesTeam(issue LocalBoardIssue, teamName string) bool {
	assignee := strings.TrimSpace(issue.Assignee)
	if assignee == "" {
		return true
	}
	return assignee == teamName || normalizeLocalTeamName(assignee) == normalizeLocalTeamName(teamName)
}

func normalizeLocalTeamName(raw string) string {
	normalized := localBoardTeamPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	return strings.Trim(normalized, "-")
}

func checkLocalTrackerContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}

func readJSONFile(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %s: %w", path, err)
	}

	// A unique temp name per writer: with a fixed "<path>.tmp" two concurrent
	// writers (daemon + CLI) truncate each other's temp file and one renames
	// a half-written or foreign payload into place.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tempPath := tmp.Name()

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("writing temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("closing temp file for %s: %w", path, err)
	}
	// CreateTemp uses 0600; keep board files group/world-readable as before.
	if err := os.Chmod(tempPath, 0o644); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("chmod temp file for %s: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("renaming temp file for %s: %w", path, err)
	}

	return nil
}

func appendJSONLine(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %s: %w", path, err)
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s for append: %w", path, err)
	}
	defer file.Close()

	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("appending %s: %w", path, err)
	}

	return nil
}
