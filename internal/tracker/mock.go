package tracker

import (
	"context"
	"sync"

	"github.com/junhoyeo/symphony-charm/internal/types"
)

// MockTracker is an in-memory implementation of the Tracker interface for testing.
type MockTracker struct {
	mu       sync.Mutex
	Issues   []types.Issue
	Comments map[string][]string          // issueID -> comments
	Claimed  map[string]bool               // issueID -> claimed
	States   map[string]types.IssueState   // issueID -> state

	// Error fields for injecting errors in tests.
	FetchErr   error
	ClaimErr   error
	ReleaseErr error
	UpdateErr  error
	CommentErr error
}

// Compile-time interface satisfaction check.
var _ Tracker = (*MockTracker)(nil)

// NewMockTracker creates a new MockTracker with initialized maps.
func NewMockTracker() *MockTracker {
	return &MockTracker{
		Issues:   []types.Issue{},
		Comments: make(map[string][]string),
		Claimed:  make(map[string]bool),
		States:   make(map[string]types.IssueState),
	}
}

// FetchIssues returns the stored issues or the configured error.
func (m *MockTracker) FetchIssues(_ context.Context) ([]types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FetchErr != nil {
		return nil, m.FetchErr
	}
	result := make([]types.Issue, len(m.Issues))
	copy(result, m.Issues)
	return result, nil
}

// ClaimIssue marks the issue as claimed or returns the configured error.
func (m *MockTracker) ClaimIssue(_ context.Context, issueID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClaimErr != nil {
		return m.ClaimErr
	}
	m.Claimed[issueID] = true
	return nil
}

// ReleaseIssue removes the claim on the issue or returns the configured error.
func (m *MockTracker) ReleaseIssue(_ context.Context, issueID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReleaseErr != nil {
		return m.ReleaseErr
	}
	delete(m.Claimed, issueID)
	return nil
}

// UpdateIssueState stores the new state or returns the configured error.
func (m *MockTracker) UpdateIssueState(_ context.Context, issueID string, state types.IssueState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UpdateErr != nil {
		return m.UpdateErr
	}
	m.States[issueID] = state
	return nil
}

// PostComment appends the comment body or returns the configured error.
func (m *MockTracker) PostComment(_ context.Context, issueID string, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CommentErr != nil {
		return m.CommentErr
	}
	m.Comments[issueID] = append(m.Comments[issueID], body)
	return nil
}
