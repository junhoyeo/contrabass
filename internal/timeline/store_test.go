package timeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreAppendAndReduce(t *testing.T) {
	store := NewStore(t.TempDir())
	issueID := "ENG-1"

	require.NoError(t, store.AppendRunUpsert(issueID, WorkflowRunSummary{
		IssueID: issueID,
		RunID:   "run-1",
		Status:  "running",
	}))
	require.NoError(t, store.AppendNodeUpsert(issueID, WorkflowNodeSummary{
		IssueID:     issueID,
		RunID:       "run-1",
		NodeID:      "node-1",
		Attempt:     1,
		Status:      "completed",
		ContentHash: "hash-1",
		Summary:     "first",
	}))
	require.NoError(t, store.AppendRunSyncUpsert(issueID, RunSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		Target:  "linear",
		Status:  "pending",
	}))
	require.NoError(t, store.AppendNodeSyncUpsert(issueID, NodeSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		NodeID:  "node-1",
		Attempt: 1,
		Target:  "linear",
		Status:  "synced",
	}))

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.Runs, 1)
	require.Len(t, snapshot.Nodes, 1)
	require.Len(t, snapshot.RunSyncStates, 1)
	require.Len(t, snapshot.NodeSyncStates, 1)
	assert.Equal(t, "run-1", snapshot.Runs[0].RunID)
	assert.Equal(t, "hash-1", snapshot.Nodes[0].ContentHash)
}

func TestStoreIdempotentNodeWritesByContentHash(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	issueID := "ENG-2"

	first := WorkflowNodeSummary{
		IssueID:     issueID,
		RunID:       "run-1",
		NodeID:      "node-1",
		Attempt:     1,
		Status:      "completed",
		ContentHash: "same-hash",
		Summary:     "first",
	}
	second := first
	second.Summary = "second"

	require.NoError(t, store.AppendNodeUpsert(issueID, first))
	require.NoError(t, store.AppendNodeUpsert(issueID, second))

	data, err := os.ReadFile(filepath.Join(dir, issueID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, strings.Split(strings.TrimSpace(string(data)), "\n"), 1)

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.Nodes, 1)
	assert.Equal(t, "first", snapshot.Nodes[0].Summary)
	assert.Equal(t, "same-hash", snapshot.Nodes[0].ContentHash)
}

func TestStoreChangedHashReplacement(t *testing.T) {
	store := NewStore(t.TempDir())
	issueID := "ENG-3"

	require.NoError(t, store.AppendNodeUpsert(issueID, WorkflowNodeSummary{
		IssueID:     issueID,
		RunID:       "run-1",
		NodeID:      "node-1",
		Attempt:     1,
		Status:      "completed",
		ContentHash: "hash-1",
		Summary:     "old",
	}))
	require.NoError(t, store.AppendNodeUpsert(issueID, WorkflowNodeSummary{
		IssueID:     issueID,
		RunID:       "run-1",
		NodeID:      "node-1",
		Attempt:     1,
		Status:      "completed",
		ContentHash: "hash-2",
		Summary:     "new",
	}))

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.Nodes, 1)
	assert.Equal(t, "new", snapshot.Nodes[0].Summary)
	assert.Equal(t, "hash-2", snapshot.Nodes[0].ContentHash)
}

func TestStoreRestartLoadAndMalformedRecords(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	issueID := "ENG-4"
	path := filepath.Join(dir, issueID+".jsonl")

	raw := strings.Join([]string{
		`{"type":"run_upsert","timestamp":"2026-05-07T20:00:00Z","run_summary":{"issue_id":"ENG-4","run_id":"run-1","status":"running"}}`,
		`{"type":"not-json"`,
		`{"type":"node_upsert","timestamp":"2026-05-07T20:00:01Z","node_summary":{"issue_id":"ENG-4","run_id":"run-1","node_id":"node-1","attempt":1,"status":"completed","content_hash":"hash-1","summary":"done"}}`,
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.Runs, 1)
	require.Len(t, snapshot.Nodes, 1)
	assert.Equal(t, "run-1", snapshot.Runs[0].RunID)
	assert.Equal(t, "hash-1", snapshot.Nodes[0].ContentHash)
}

func TestStoreSyncStateKeysIncludeTarget(t *testing.T) {
	store := NewStore(t.TempDir())
	issueID := "ENG-6"

	require.NoError(t, store.AppendRunSyncUpsert(issueID, RunSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		Target:  "linear",
		Status:  "synced",
	}))
	require.NoError(t, store.AppendRunSyncUpsert(issueID, RunSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		Target:  "slack",
		Status:  "pending",
	}))
	require.NoError(t, store.AppendNodeSyncUpsert(issueID, NodeSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		NodeID:  "node-1",
		Attempt: 1,
		Target:  "linear",
		Status:  "synced",
	}))
	require.NoError(t, store.AppendNodeSyncUpsert(issueID, NodeSyncState{
		IssueID: issueID,
		RunID:   "run-1",
		NodeID:  "node-1",
		Attempt: 1,
		Target:  "slack",
		Status:  "pending",
	}))

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.RunSyncStates, 2)
	require.Len(t, snapshot.NodeSyncStates, 2)
	assert.Equal(t, "linear", snapshot.RunSyncStates[0].Target)
	assert.Equal(t, "slack", snapshot.RunSyncStates[1].Target)
}

func TestStoreConcurrentValidJSONLWrites(t *testing.T) {
	store := NewStore(t.TempDir())
	issueID := "ENG-5"

	const writers = 24
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- store.AppendNodeUpsert(issueID, WorkflowNodeSummary{
				IssueID:     issueID,
				RunID:       "run-1",
				NodeID:      "node-1",
				Attempt:     i,
				Status:      "completed",
				ContentHash: "hash-" + strings.TrimSpace(string(rune('a'+(i%10)))),
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	path := filepath.Join(store.baseDir, issueID+".jsonl")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, writers)

	for _, line := range lines {
		var rec timelineRecord
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		assert.Equal(t, recordNodeUpsert, rec.Type)
	}

	snapshot, err := store.LoadSnapshot(issueID)
	require.NoError(t, err)
	require.Len(t, snapshot.Nodes, writers)
}

func TestStoreLoadSnapshotEmpty(t *testing.T) {
	store := NewStore(t.TempDir())

	snapshot, err := store.LoadSnapshot("missing")
	require.NoError(t, err)
	assert.Equal(t, "missing", snapshot.IssueID)
	assert.NotNil(t, snapshot.RunsByID)
	assert.NotNil(t, snapshot.NodesByKey)
	assert.NotNil(t, snapshot.RunSyncByID)
	assert.NotNil(t, snapshot.NodeSyncByKey)
	assert.WithinDuration(t, time.Now(), snapshot.GeneratedAt, 2*time.Second)
}

func TestRenderNodeCommentBodyIncludesHiddenMarker(t *testing.T) {
	body := RenderNodeCommentBody(WorkflowNodeSummary{
		IssueID:     "ENG-7",
		RunID:       "run-1",
		NodeID:      "run:attempt-1:complete",
		Attempt:     1,
		Status:      "completed",
		ContentHash: "hash-1",
		Summary:     "Tests passed.",
	})

	assert.Contains(t, body, "Contrabass workflow node")
	assert.Contains(t, body, "Tests passed.")
	assert.Contains(t, body, "<!-- contrabass:workflow-node")
	assert.Contains(t, body, `issue_id="ENG-7"`)
	assert.Contains(t, body, `run_id="run-1"`)
	assert.Contains(t, body, `node_id="run:attempt-1:complete"`)
	assert.Contains(t, body, `attempt="1"`)
	assert.Contains(t, body, `content_hash="hash-1"`)
}
