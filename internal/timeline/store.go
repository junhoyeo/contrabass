package timeline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Store persists workflow timeline records as append-only JSONL files.
type Store struct {
	baseDir string
	mu      sync.Mutex
}

// NewStore creates a timeline store rooted at baseDir.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

func (s *Store) path(issueID string) string {
	return filepath.Join(s.baseDir, issueID+".jsonl")
}

func (s *Store) AppendRunUpsert(issueID string, summary WorkflowRunSummary) error {
	return s.append(issueID, timelineRecord{
		Type:       recordRunUpsert,
		Timestamp:  time.Now().UTC(),
		RunSummary: &summary,
	})
}

func (s *Store) AppendNodeUpsert(issueID string, summary WorkflowNodeSummary) error {
	summary = EnsureNodeHash(summary)
	return s.append(issueID, timelineRecord{
		Type:        recordNodeUpsert,
		Timestamp:   time.Now().UTC(),
		NodeSummary: &summary,
	})
}

func (s *Store) AppendRunSyncUpsert(issueID string, state RunSyncState) error {
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	return s.append(issueID, timelineRecord{
		Type:      recordRunSyncUpsert,
		Timestamp: time.Now().UTC(),
		RunSync:   &state,
	})
}

func (s *Store) AppendNodeSyncUpsert(issueID string, state NodeSyncState) error {
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	return s.append(issueID, timelineRecord{
		Type:      recordNodeSyncUpsert,
		Timestamp: time.Now().UTC(),
		NodeSync:  &state,
	})
}

func (s *Store) UpsertRun(ctx context.Context, summary WorkflowRunSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.AppendRunUpsert(summary.IssueID, summary)
}

func (s *Store) UpsertNode(ctx context.Context, summary WorkflowNodeSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.AppendNodeUpsert(summary.IssueID, summary)
}

func (s *Store) UpsertRunSync(ctx context.Context, state RunSyncState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.AppendRunSyncUpsert(state.IssueID, state)
}

func (s *Store) UpsertNodeSync(ctx context.Context, state NodeSyncState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.AppendNodeSyncUpsert(state.IssueID, state)
}

func (s *Store) append(issueID string, record timelineRecord) error {
	path := s.path(issueID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create timeline dir: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	lock, err := newFileLock(path)
	if err != nil {
		return err
	}
	if err := lock.lock(); err != nil {
		return fmt.Errorf("acquire timeline lock for %s: %w", path, err)
	}
	defer lock.unlock()

	if record.Type == recordNodeUpsert && record.NodeSummary != nil {
		snapshot, err := s.loadSnapshotNoLock(issueID, path)
		if err != nil {
			return err
		}
		if existing, ok := snapshot.NodesByKey[nodeKey(*record.NodeSummary)]; ok && existing.ContentHash == record.NodeSummary.ContentHash {
			return nil
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal timeline record: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open timeline file %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append timeline record: %w", err)
	}
	return nil
}

// LoadSnapshot reduces the issue timeline file into a materialized snapshot.
func (s *Store) LoadSnapshot(issueID string) (*WorkflowTimelineSnapshot, error) {
	path := s.path(issueID)
	lock, err := newFileLock(path)
	if err != nil {
		return nil, err
	}
	if err := lock.lock(); err != nil {
		return nil, fmt.Errorf("acquire timeline lock for %s: %w", path, err)
	}
	defer lock.unlock()

	return s.loadSnapshotNoLock(issueID, path)
}

func (s *Store) IssueTimeline(ctx context.Context, issueID string) (*WorkflowTimelineSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.LoadSnapshot(issueID)
}

func (s *Store) Snapshot(ctx context.Context, issueID string) (WorkflowTimelineSnapshot, error) {
	snapshot, err := s.IssueTimeline(ctx, issueID)
	if err != nil {
		return WorkflowTimelineSnapshot{}, err
	}
	if snapshot == nil {
		return WorkflowTimelineSnapshot{}, nil
	}
	return *snapshot, nil
}

func (s *Store) ListIssueIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read timeline dir: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".jsonl"))
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) loadSnapshotNoLock(issueID, path string) (*WorkflowTimelineSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newSnapshot(issueID), nil
		}
		return nil, fmt.Errorf("open timeline file %s: %w", path, err)
	}
	defer f.Close()

	snapshot := newSnapshot(issueID)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec timelineRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		snapshot.apply(rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan timeline file %s: %w", path, err)
	}

	snapshot.finalize()
	return snapshot, nil
}

func newSnapshot(issueID string) *WorkflowTimelineSnapshot {
	return &WorkflowTimelineSnapshot{
		IssueID:       issueID,
		RunsByID:      map[string]WorkflowRunSummary{},
		NodesByKey:    map[string]WorkflowNodeSummary{},
		RunSyncByID:   map[string]RunSyncState{},
		NodeSyncByKey: map[string]NodeSyncState{},
		GeneratedAt:   time.Now().UTC(),
	}
}

type fileLock struct {
	path string
	file *os.File
}

func newFileLock(path string) (*fileLock, error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	return &fileLock{path: lockPath, file: f}, nil
}

func (l *fileLock) lock() error {
	return syscall.Flock(int(l.file.Fd()), syscall.LOCK_EX)
}

func (l *fileLock) unlock() error {
	defer l.file.Close()
	return syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
}

const (
	recordRunUpsert      = "run_upsert"
	recordNodeUpsert     = "node_upsert"
	recordRunSyncUpsert  = "run_sync_upsert"
	recordNodeSyncUpsert = "node_sync_upsert"
)

type timelineRecord struct {
	Type        string               `json:"type"`
	Timestamp   time.Time            `json:"timestamp"`
	RunSummary  *WorkflowRunSummary  `json:"run_summary,omitempty"`
	NodeSummary *WorkflowNodeSummary `json:"node_summary,omitempty"`
	RunSync     *RunSyncState        `json:"run_sync,omitempty"`
	NodeSync    *NodeSyncState       `json:"node_sync,omitempty"`
}

func (s *WorkflowTimelineSnapshot) apply(rec timelineRecord) {
	switch rec.Type {
	case recordRunUpsert:
		if rec.RunSummary != nil {
			s.RunsByID[rec.RunSummary.RunID] = *rec.RunSummary
		}
	case recordNodeUpsert:
		if rec.NodeSummary != nil {
			s.NodesByKey[nodeKey(*rec.NodeSummary)] = *rec.NodeSummary
		}
	case recordRunSyncUpsert:
		if rec.RunSync != nil {
			s.RunSyncByID[runSyncKey(*rec.RunSync)] = *rec.RunSync
		}
	case recordNodeSyncUpsert:
		if rec.NodeSync != nil {
			s.NodeSyncByKey[nodeSyncKey(*rec.NodeSync)] = *rec.NodeSync
		}
	}
}

func (s *WorkflowTimelineSnapshot) finalize() {
	for _, run := range s.RunsByID {
		s.Runs = append(s.Runs, run)
	}
	for _, node := range s.NodesByKey {
		s.Nodes = append(s.Nodes, node)
	}
	for _, runSync := range s.RunSyncByID {
		s.RunSyncStates = append(s.RunSyncStates, runSync)
	}
	for _, nodeSync := range s.NodeSyncByKey {
		s.NodeSyncStates = append(s.NodeSyncStates, nodeSync)
	}

	sort.Slice(s.Runs, func(i, j int) bool { return s.Runs[i].RunID < s.Runs[j].RunID })
	sort.Slice(s.Nodes, func(i, j int) bool {
		if s.Nodes[i].RunID != s.Nodes[j].RunID {
			return s.Nodes[i].RunID < s.Nodes[j].RunID
		}
		if s.Nodes[i].NodeID != s.Nodes[j].NodeID {
			return s.Nodes[i].NodeID < s.Nodes[j].NodeID
		}
		return s.Nodes[i].Attempt < s.Nodes[j].Attempt
	})
	sort.Slice(s.RunSyncStates, func(i, j int) bool {
		ki := runSyncKey(s.RunSyncStates[i])
		kj := runSyncKey(s.RunSyncStates[j])
		return ki < kj
	})
	sort.Slice(s.NodeSyncStates, func(i, j int) bool {
		ki := nodeSyncKey(s.NodeSyncStates[i])
		kj := nodeSyncKey(s.NodeSyncStates[j])
		return ki < kj
	})
}

func nodeKey(n WorkflowNodeSummary) string {
	return strings.Join([]string{n.IssueID, n.RunID, n.NodeID, fmt.Sprint(n.Attempt)}, ":")
}

func runSyncKey(n RunSyncState) string {
	return strings.Join([]string{n.IssueID, n.RunID, n.Target}, ":")
}

func nodeSyncKey(n NodeSyncState) string {
	return strings.Join([]string{n.IssueID, n.RunID, n.NodeID, fmt.Sprint(n.Attempt), n.Target}, ":")
}
