package timeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLinearWriter struct {
	mu        sync.Mutex
	roots     []tracker.RootCommentInput
	replies   []tracker.ReplyCommentInput
	updates   []string
	replyErr  error
	updateErr error
}

func (w *fakeLinearWriter) CreateRootComment(_ context.Context, input tracker.RootCommentInput) (tracker.CommentRef, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.roots = append(w.roots, input)
	return tracker.CommentRef{ID: "root-" + string(rune('0'+len(w.roots))), URL: "https://linear.app/root"}, nil
}

func (w *fakeLinearWriter) CreateReplyComment(_ context.Context, input tracker.ReplyCommentInput) (tracker.CommentRef, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.replyErr != nil {
		return tracker.CommentRef{}, w.replyErr
	}
	w.replies = append(w.replies, input)
	return tracker.CommentRef{ID: "reply-" + string(rune('0'+len(w.replies))), URL: "https://linear.app/reply"}, nil
}

func (w *fakeLinearWriter) UpdateComment(_ context.Context, commentID string, _ string) (tracker.CommentRef, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.updateErr != nil {
		return tracker.CommentRef{}, w.updateErr
	}
	w.updates = append(w.updates, commentID)
	return tracker.CommentRef{ID: commentID, URL: "https://linear.app/updated"}, nil
}

func seedRunAndNodes(t *testing.T, store *Store, nodes ...WorkflowNodeSummary) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.UpsertRun(ctx, WorkflowRunSummary{
		IssueID:         "issue-1",
		IssueIdentifier: "ENG-1",
		RunID:           RunID(1),
		Attempt:         1,
		Status:          NodeStatusStarted,
		StartedAt:       time.Now(),
	}))
	for _, node := range nodes {
		require.NoError(t, store.UpsertNode(ctx, node))
	}
}

func syncableNode(id string) WorkflowNodeSummary {
	return EnsureNodeHash(WorkflowNodeSummary{
		IssueID:     "issue-1",
		RunID:       RunID(1),
		NodeID:      NodeID(1, id),
		Attempt:     1,
		Kind:        id,
		Status:      NodeStatusSucceeded,
		Title:       id,
		Summary:     "summary " + id,
		Syncable:    true,
		CompletedAt: time.Now(),
	})
}

func TestLinearSyncerCreatesOneRootForSameRun(t *testing.T) {
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, syncableNode("complete"), syncableNode("retry-queued"))
	writer := &fakeLinearWriter{}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	assert.Len(t, writer.roots, 1)
	assert.Len(t, writer.replies, 2)
	assert.Equal(t, writer.roots[0].IssueID, writer.replies[0].IssueID)
	snapshot, err := store.Snapshot(context.Background(), "issue-1")
	require.NoError(t, err)
	assert.Len(t, snapshot.RunSyncStates, 1)
	assert.Len(t, snapshot.NodeSyncStates, 2)
}

func TestLinearSyncerReusesRootAfterRestart(t *testing.T) {
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, syncableNode("complete"))
	require.NoError(t, store.UpsertRunSync(context.Background(), RunSyncState{
		IssueID: "issue-1", RunID: RunID(1), Target: SyncTargetLinear,
		Status: SyncStatusSynced, CommentID: "existing-root", UpdatedAt: time.Now(),
	}))
	writer := &fakeLinearWriter{}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	assert.Empty(t, writer.roots)
	require.Len(t, writer.replies, 1)
	assert.Equal(t, "existing-root", writer.replies[0].ParentID)
}

func TestLinearSyncerDedupesSyncedNodeAfterRestart(t *testing.T) {
	node := syncableNode("complete")
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, node)
	require.NoError(t, store.UpsertRunSync(context.Background(), RunSyncState{
		IssueID: "issue-1", RunID: RunID(1), Target: SyncTargetLinear,
		Status: SyncStatusSynced, CommentID: "existing-root", UpdatedAt: time.Now(),
	}))
	require.NoError(t, store.UpsertNodeSync(context.Background(), NodeSyncState{
		IssueID: "issue-1", RunID: RunID(1), NodeID: node.NodeID, Attempt: node.Attempt, Target: SyncTargetLinear,
		ContentHash: node.ContentHash, Status: SyncStatusSynced, CommentID: "existing-node", UpdatedAt: time.Now(),
	}))
	writer := &fakeLinearWriter{}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	assert.Empty(t, writer.roots)
	assert.Empty(t, writer.replies)
	assert.Empty(t, writer.updates)
}

func TestLinearSyncerPersistsRetryAfterOnRateLimit(t *testing.T) {
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, syncableNode("complete"))
	writer := &fakeLinearWriter{replyErr: &tracker.RateLimitError{RetryAfter: time.Minute}}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	snapshot, err := store.Snapshot(context.Background(), "issue-1")
	require.NoError(t, err)
	require.Len(t, snapshot.NodeSyncStates, 1)
	assert.Equal(t, SyncStatusFailed, snapshot.NodeSyncStates[0].Status)
	assert.True(t, snapshot.NodeSyncStates[0].RetryAfter.After(time.Now()))
}

func TestLinearSyncerFallsBackToTopLevelWhenDefaultRepliesUnsupported(t *testing.T) {
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, syncableNode("complete"))
	writer := &fakeLinearWriter{replyErr: tracker.ErrLinearReplyCommentsUnsupported}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{AllowReplyFallback: true, PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	assert.Len(t, writer.roots, 2) // run root plus node top-level fallback
	assert.Empty(t, writer.replies)
}

func TestLinearSyncerAppendFallbackWhenUpdateUnsupported(t *testing.T) {
	oldNode := syncableNode("complete")
	newNode := oldNode
	newNode.Summary = "changed"
	newNode.ContentHash = ""
	newNode = EnsureNodeHash(newNode)
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, newNode)
	require.NoError(t, store.UpsertRunSync(context.Background(), RunSyncState{
		IssueID: "issue-1", RunID: RunID(1), Target: SyncTargetLinear,
		Status: SyncStatusSynced, CommentID: "existing-root", UpdatedAt: time.Now(),
	}))
	require.NoError(t, store.UpsertNodeSync(context.Background(), NodeSyncState{
		IssueID: "issue-1", RunID: RunID(1), NodeID: newNode.NodeID, Attempt: newNode.Attempt, Target: SyncTargetLinear,
		ContentHash: oldNode.ContentHash, Status: SyncStatusSynced, CommentID: "existing-node", UpdatedAt: time.Now(),
	}))
	writer := &fakeLinearWriter{updateErr: tracker.ErrLinearCommentUpdateUnsupported}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	assert.Empty(t, writer.updates)
	assert.Len(t, writer.replies, 1)
}

func TestLinearSyncerRecordsNonFallbackReplyErrors(t *testing.T) {
	store := NewStore(t.TempDir())
	seedRunAndNodes(t, store, syncableNode("complete"))
	writer := &fakeLinearWriter{replyErr: errors.New("linear down")}
	syncer := NewLinearSyncer(store, writer, LinearSyncerConfig{PollInterval: time.Hour})

	require.NoError(t, syncer.ProcessIssue(context.Background(), "issue-1"))

	snapshot, err := store.Snapshot(context.Background(), "issue-1")
	require.NoError(t, err)
	require.Len(t, snapshot.NodeSyncStates, 1)
	assert.Contains(t, snapshot.NodeSyncStates[0].LastError, "linear down")
}
