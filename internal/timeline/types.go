package timeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	SyncTargetLinear = "linear"

	CommentModeReplyThread = "reply_thread"
	CommentModeTopLevel    = "top_level"

	NodeStatusStarted     = "started"
	NodeStatusSucceeded   = "completed"
	NodeStatusFailed      = "failed"
	NodeStatusNeedsReview = "needs_review"
	NodeStatusRetryQueued = "retry_queued"

	SyncStatusPending = "pending"
	SyncStatusSynced  = "synced"
	SyncStatusFailed  = "failed"
)

// WorkflowTimelineSnapshot is the materialized view of a per-issue timeline.
type WorkflowTimelineSnapshot struct {
	IssueID        string                `json:"issue_id"`
	Runs           []WorkflowRunSummary  `json:"runs"`
	Nodes          []WorkflowNodeSummary `json:"nodes"`
	RunSyncStates  []RunSyncState        `json:"run_sync_states"`
	NodeSyncStates []NodeSyncState       `json:"node_sync_states"`
	GeneratedAt    time.Time             `json:"generated_at"`

	RunsByID      map[string]WorkflowRunSummary  `json:"-"`
	NodesByKey    map[string]WorkflowNodeSummary `json:"-"`
	RunSyncByID   map[string]RunSyncState        `json:"-"`
	NodeSyncByKey map[string]NodeSyncState       `json:"-"`
}

// WorkflowRunSummary summarizes a single workflow run.
type WorkflowRunSummary struct {
	IssueID         string    `json:"issue_id"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	RunID           string    `json:"run_id"`
	Attempt         int       `json:"attempt,omitempty"`
	Status          string    `json:"status"`
	Title           string    `json:"title,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	URL             string    `json:"url,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	FinishedAt      time.Time `json:"completed_at,omitempty"`
}

// WorkflowNodeSummary summarizes one node attempt in a workflow run.
type WorkflowNodeSummary struct {
	IssueID     string         `json:"issue_id"`
	RunID       string         `json:"run_id"`
	NodeID      string         `json:"node_id"`
	Attempt     int            `json:"attempt"`
	Kind        string         `json:"kind,omitempty"`
	Status      string         `json:"status"`
	Title       string         `json:"title,omitempty"`
	Body        string         `json:"body,omitempty"`
	ContentHash string         `json:"content_hash"`
	Summary     string         `json:"summary,omitempty"`
	Error       string         `json:"error,omitempty"`
	Syncable    bool           `json:"syncable,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	CompletedAt time.Time      `json:"completed_at,omitempty"`
	RuntimeMs   int            `json:"runtime_ms,omitempty"`
	TokensIn    int64          `json:"tokens_in,omitempty"`
	TokensOut   int64          `json:"tokens_out,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// RunSyncState tracks comment sync state for a run.
type RunSyncState struct {
	IssueID    string    `json:"issue_id"`
	RunID      string    `json:"run_id"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	CommentID  string    `json:"comment_id,omitempty"`
	CommentURL string    `json:"comment_url,omitempty"`
	RetryAfter time.Time `json:"retry_after,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// NodeSyncState tracks comment sync state for a node.
type NodeSyncState struct {
	IssueID     string    `json:"issue_id"`
	RunID       string    `json:"run_id"`
	NodeID      string    `json:"node_id"`
	Attempt     int       `json:"attempt"`
	Target      string    `json:"target"`
	ContentHash string    `json:"content_hash,omitempty"`
	Status      string    `json:"status"`
	CommentID   string    `json:"comment_id,omitempty"`
	CommentURL  string    `json:"comment_url,omitempty"`
	RetryAfter  time.Time `json:"retry_after,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

func RunID(attempt int) string {
	if attempt <= 0 {
		attempt = 1
	}
	return fmt.Sprintf("run:attempt-%d", attempt)
}

func NodeID(attempt int, suffix string) string {
	if attempt <= 0 {
		attempt = 1
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		suffix = "unknown"
	}
	return fmt.Sprintf("%s:%s", RunID(attempt), suffix)
}

func EnsureNodeHash(node WorkflowNodeSummary) WorkflowNodeSummary {
	if strings.TrimSpace(node.ContentHash) != "" {
		return node
	}
	payload := struct {
		IssueID   string `json:"issue_id"`
		RunID     string `json:"run_id"`
		NodeID    string `json:"node_id"`
		Attempt   int    `json:"attempt"`
		Kind      string `json:"kind,omitempty"`
		Status    string `json:"status"`
		Title     string `json:"title,omitempty"`
		Body      string `json:"body,omitempty"`
		Summary   string `json:"summary,omitempty"`
		Error     string `json:"error,omitempty"`
		TokensIn  int64  `json:"tokens_in,omitempty"`
		TokensOut int64  `json:"tokens_out,omitempty"`
	}{
		IssueID:   node.IssueID,
		RunID:     node.RunID,
		NodeID:    node.NodeID,
		Attempt:   node.Attempt,
		Kind:      node.Kind,
		Status:    node.Status,
		Title:     node.Title,
		Body:      node.Body,
		Summary:   node.Summary,
		Error:     node.Error,
		TokensIn:  node.TokensIn,
		TokensOut: node.TokensOut,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(fmt.Sprintf("%s:%s:%s:%d:%s:%s:%s:%s", node.IssueID, node.RunID, node.NodeID, node.Attempt, node.Status, node.Title, node.Summary, node.Error))
	}
	sum := sha256.Sum256(data)
	node.ContentHash = hex.EncodeToString(sum[:])
	return node
}

const nodeMarkerName = "contrabass:workflow-node"

// RenderNodeCommentBody renders a Linear-safe workflow node summary with a
// hidden marker used by the syncer for idempotency across restarts.
func RenderNodeCommentBody(node WorkflowNodeSummary) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Contrabass workflow node `%s` is `%s`.", node.NodeID, node.Status))
	if node.Summary != "" {
		b.WriteString("\n\n")
		b.WriteString(node.Summary)
	}
	b.WriteString("\n\n")
	b.WriteString(HiddenNodeMarker(node))
	return b.String()
}

func HiddenNodeMarker(node WorkflowNodeSummary) string {
	return fmt.Sprintf(
		`<!-- %s issue_id="%s" run_id="%s" node_id="%s" attempt="%d" content_hash="%s" -->`,
		nodeMarkerName,
		markerValue(node.IssueID),
		markerValue(node.RunID),
		markerValue(node.NodeID),
		node.Attempt,
		markerValue(node.ContentHash),
	)
}

func markerValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "--", "- -")
}
