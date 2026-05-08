package tracker

import (
	"context"

	"github.com/junhoyeo/contrabass/internal/types"
)

// Tracker defines the interface for interacting with an issue tracker.
type Tracker interface {
	// FetchIssues retrieves candidate issues from the tracker.
	FetchIssues(ctx context.Context) ([]types.Issue, error)

	// ClaimIssue marks an issue as claimed in the tracker.
	ClaimIssue(ctx context.Context, issueID string) error

	// ReleaseIssue removes the claim on an issue in the tracker.
	ReleaseIssue(ctx context.Context, issueID string) error

	// UpdateIssueState updates the state of an issue in the tracker.
	UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error

	// PostComment posts a comment on an issue in the tracker.
	PostComment(ctx context.Context, issueID string, body string) error
}

// CommentRef identifies a tracker comment created for outbound projections.
type CommentRef struct {
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
}

// RootCommentInput creates a top-level issue comment.
type RootCommentInput struct {
	IssueID string
	Body    string
}

// ReplyCommentInput creates a child comment when the tracker supports threads.
type ReplyCommentInput struct {
	IssueID  string
	ParentID string
	Body     string
}

// LinearCommentWriter is an optional tracker capability for ID-returning
// Linear comment writes. The core Tracker interface intentionally stays small.
type LinearCommentWriter interface {
	CreateRootComment(ctx context.Context, input RootCommentInput) (CommentRef, error)
	CreateReplyComment(ctx context.Context, input ReplyCommentInput) (CommentRef, error)
	UpdateComment(ctx context.Context, commentID string, body string) (CommentRef, error)
}

// LinearMarker identifies tracker implementations backed by Linear.
type LinearMarker interface {
	IsLinearTracker() bool
}

// IsLinearTracker reports whether a tracker is backed by Linear.
func IsLinearTracker(t Tracker) bool {
	marker, ok := t.(LinearMarker)
	return ok && marker.IsLinearTracker()
}
