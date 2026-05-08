package timeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"

	"github.com/junhoyeo/contrabass/internal/tracker"
)

type LinearSyncerConfig struct {
	Target             string
	Mode               string
	AllowReplyFallback bool
	QueueSize          int
	PollInterval       time.Duration
	Logger             *log.Logger
}

type LinearSyncer struct {
	store  *Store
	writer tracker.LinearCommentWriter
	cfg    LinearSyncerConfig
	queue  chan string
}

func NewLinearSyncer(store *Store, writer tracker.LinearCommentWriter, cfg LinearSyncerConfig) *LinearSyncer {
	if cfg.Target == "" {
		cfg.Target = SyncTargetLinear
	}
	if cfg.Mode == "" {
		cfg.Mode = CommentModeReplyThread
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 100
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	return &LinearSyncer{
		store:  store,
		writer: writer,
		cfg:    cfg,
		queue:  make(chan string, cfg.QueueSize),
	}
}

func (s *LinearSyncer) Notify(issueID string) {
	if s == nil || issueID == "" {
		return
	}
	select {
	case s.queue <- issueID:
	default:
	}
}

func (s *LinearSyncer) Run(ctx context.Context) error {
	if s == nil || s.store == nil || s.writer == nil {
		return nil
	}
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.Drain(context.Background())
		case issueID := <-s.queue:
			if err := s.ProcessIssue(ctx, issueID); err != nil && s.cfg.Logger != nil {
				s.cfg.Logger.Warn("linear timeline sync failed", "issue_id", issueID, "err", err)
			}
		case <-ticker.C:
			if err := s.ProcessAll(ctx); err != nil && s.cfg.Logger != nil {
				s.cfg.Logger.Warn("linear timeline scan failed", "err", err)
			}
		}
	}
}

func (s *LinearSyncer) Drain(ctx context.Context) error {
	if s == nil {
		return nil
	}
	for {
		select {
		case issueID := <-s.queue:
			if err := s.ProcessIssue(ctx, issueID); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (s *LinearSyncer) ProcessAll(ctx context.Context) error {
	ids, err := s.store.ListIssueIDs(ctx)
	if err != nil {
		return err
	}
	for _, issueID := range ids {
		if err := s.ProcessIssue(ctx, issueID); err != nil {
			return err
		}
	}
	return nil
}

func (s *LinearSyncer) ProcessIssue(ctx context.Context, issueID string) error {
	if s == nil || s.store == nil || s.writer == nil {
		return nil
	}
	snapshot, err := s.store.Snapshot(ctx, issueID)
	if err != nil {
		return err
	}
	runs := map[string]WorkflowRunSummary{}
	for _, run := range snapshot.Runs {
		runs[run.RunID] = run
	}
	runSyncs := map[string]RunSyncState{}
	for _, state := range snapshot.RunSyncStates {
		if state.Target == s.cfg.Target {
			runSyncs[state.RunID] = state
		}
	}
	nodeSyncs := map[string]NodeSyncState{}
	for _, state := range snapshot.NodeSyncStates {
		if state.Target == s.cfg.Target {
			nodeSyncs[nodeSyncLookupKey(state.RunID, state.NodeID, state.Attempt)] = state
		}
	}
	for _, node := range snapshot.Nodes {
		if !node.Syncable {
			continue
		}
		node = EnsureNodeHash(node)
		state := nodeSyncs[nodeSyncLookupKey(node.RunID, node.NodeID, node.Attempt)]
		if state.Status == SyncStatusSynced && state.ContentHash == node.ContentHash {
			continue
		}
		if !state.RetryAfter.IsZero() && state.RetryAfter.After(time.Now()) {
			continue
		}
		run := runs[node.RunID]
		if run.IssueID == "" {
			run = WorkflowRunSummary{IssueID: node.IssueID, RunID: node.RunID, Attempt: node.Attempt, Status: node.Status, StartedAt: node.StartedAt}
		}
		runState, err := s.ensureRoot(ctx, run, runSyncs[node.RunID])
		if err != nil {
			return err
		}
		runSyncs[node.RunID] = runState
		if err := s.syncNode(ctx, node, runState, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *LinearSyncer) ensureRoot(ctx context.Context, run WorkflowRunSummary, state RunSyncState) (RunSyncState, error) {
	if state.Status == SyncStatusSynced && state.CommentID != "" {
		return state, nil
	}
	if !state.RetryAfter.IsZero() && state.RetryAfter.After(time.Now()) {
		return state, nil
	}
	ref, err := s.writer.CreateRootComment(ctx, tracker.RootCommentInput{IssueID: run.IssueID, Body: RenderRunRootComment(run)})
	state = RunSyncState{IssueID: run.IssueID, RunID: run.RunID, Target: s.cfg.Target, UpdatedAt: time.Now()}
	if err != nil {
		state.Status = SyncStatusFailed
		state.LastError = err.Error()
		state.Error = err.Error()
		state.RetryAfter = retryAfterTime(err)
		if upsertErr := s.store.UpsertRunSync(ctx, state); upsertErr != nil {
			return state, upsertErr
		}
		return state, nil
	}
	state.Status = SyncStatusSynced
	state.CommentID = ref.ID
	state.CommentURL = ref.URL
	return state, s.store.UpsertRunSync(ctx, state)
}

func (s *LinearSyncer) syncNode(ctx context.Context, node WorkflowNodeSummary, runState RunSyncState, state NodeSyncState) error {
	body := RenderNodeComment(node)
	var ref tracker.CommentRef
	var err error
	if state.CommentID != "" && state.ContentHash != "" && state.ContentHash != node.ContentHash {
		ref, err = s.writer.UpdateComment(ctx, state.CommentID, body)
		if err == nil {
			return s.markNodeSynced(ctx, node, ref)
		}
		if !errors.Is(err, tracker.ErrLinearCommentUpdateUnsupported) {
			return s.markNodeFailed(ctx, node, err)
		}
	}

	switch s.cfg.Mode {
	case CommentModeTopLevel:
		ref, err = s.writer.CreateRootComment(ctx, tracker.RootCommentInput{IssueID: node.IssueID, Body: body})
	default:
		ref, err = s.writer.CreateReplyComment(ctx, tracker.ReplyCommentInput{IssueID: node.IssueID, ParentID: runState.CommentID, Body: body})
		if errors.Is(err, tracker.ErrLinearReplyCommentsUnsupported) && s.cfg.AllowReplyFallback {
			ref, err = s.writer.CreateRootComment(ctx, tracker.RootCommentInput{IssueID: node.IssueID, Body: body})
		}
	}
	if err != nil {
		return s.markNodeFailed(ctx, node, err)
	}
	return s.markNodeSynced(ctx, node, ref)
}

func (s *LinearSyncer) markNodeSynced(ctx context.Context, node WorkflowNodeSummary, ref tracker.CommentRef) error {
	return s.store.UpsertNodeSync(ctx, NodeSyncState{
		IssueID:     node.IssueID,
		RunID:       node.RunID,
		NodeID:      node.NodeID,
		Attempt:     node.Attempt,
		Target:      s.cfg.Target,
		ContentHash: node.ContentHash,
		Status:      SyncStatusSynced,
		CommentID:   ref.ID,
		CommentURL:  ref.URL,
		UpdatedAt:   time.Now(),
	})
}

func (s *LinearSyncer) markNodeFailed(ctx context.Context, node WorkflowNodeSummary, cause error) error {
	state := NodeSyncState{
		IssueID:     node.IssueID,
		RunID:       node.RunID,
		NodeID:      node.NodeID,
		Attempt:     node.Attempt,
		Target:      s.cfg.Target,
		ContentHash: node.ContentHash,
		Status:      SyncStatusFailed,
		LastError:   fmt.Sprint(cause),
		Error:       fmt.Sprint(cause),
		RetryAfter:  retryAfterTime(cause),
		UpdatedAt:   time.Now(),
	}
	return s.store.UpsertNodeSync(ctx, state)
}

func nodeSyncLookupKey(runID, nodeID string, attempt int) string {
	return fmt.Sprintf("%s\x00%s\x00%d", runID, nodeID, attempt)
}

func retryAfterTime(err error) time.Time {
	var rateLimit *tracker.RateLimitError
	if errors.As(err, &rateLimit) && rateLimit.RetryAfter > 0 {
		return time.Now().Add(rateLimit.RetryAfter)
	}
	return time.Time{}
}
