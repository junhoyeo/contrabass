package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"github.com/junhoyeo/contrabass/internal/types"
)

type MockManager struct {
	baseDir string

	mu     sync.RWMutex
	active map[string]string
}

func NewMockManager(baseDir string) *MockManager {
	return &MockManager{
		baseDir: baseDir,
		active:  make(map[string]string),
	}
}

func (m *MockManager) Create(_ context.Context, issue types.Issue) (string, error) {
	if issue.ID == "" {
		return "", errors.New("issue ID is required")
	}

	workspacePath := filepath.Join(m.baseDir, "workspaces", issue.ID)
	m.mu.Lock()
	m.active[issue.ID] = workspacePath
	m.mu.Unlock()
	return workspacePath, nil
}

func (m *MockManager) Cleanup(_ context.Context, issueID string) error {
	if issueID == "" {
		return errors.New("issue ID is required")
	}
	m.mu.Lock()
	delete(m.active, issueID)
	m.mu.Unlock()
	return nil
}

func (m *MockManager) CleanupAll(_ context.Context) error {
	m.mu.Lock()
	clear(m.active)
	m.mu.Unlock()

	return nil
}

func (m *MockManager) Exists(issueID string) bool {
	m.mu.RLock()
	_, ok := m.active[issueID]
	m.mu.RUnlock()

	return ok
}

func (m *MockManager) List() []string {
	m.mu.RLock()
	issueIDs := make([]string, 0, len(m.active))
	for issueID := range m.active {
		issueIDs = append(issueIDs, issueID)
	}
	m.mu.RUnlock()

	sort.Strings(issueIDs)
	return issueIDs
}
