package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func (o *Orchestrator) recordTimelineRun(ctx context.Context, issue types.Issue, attempt int, status string, startedAt time.Time, finishedAt time.Time) {
	if o.timeline == nil || issue.ID == "" {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	run := timeline.WorkflowRunSummary{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		RunID:           timeline.RunID(attempt),
		Attempt:         attempt,
		Status:          status,
		Title:           issue.Title,
		URL:             issue.URL,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
	}
	if err := o.timeline.UpsertRun(ctx, run); err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "timeline_run_upsert_failed", "err", err)
	}
}

func (o *Orchestrator) recordTimelineNode(ctx context.Context, issue types.Issue, attempt types.RunAttempt, suffix, status, title, summary, errText string, syncable bool) {
	if o.timeline == nil || issue.ID == "" {
		return
	}
	attemptNumber := attempt.Attempt
	if attemptNumber <= 0 {
		attemptNumber = 1
	}
	now := time.Now()
	runStarted := attempt.StartTime
	if runStarted.IsZero() {
		runStarted = now
	}
	o.recordTimelineRun(ctx, issue, attemptNumber, runStatusFromNodeStatus(status), runStarted, runFinishedAt(status, now))
	node := timeline.WorkflowNodeSummary{
		IssueID:     issue.ID,
		RunID:       timeline.RunID(attemptNumber),
		NodeID:      timeline.NodeID(attemptNumber, suffix),
		Attempt:     attemptNumber,
		Kind:        suffix,
		Status:      status,
		Title:       title,
		Summary:     summary,
		Error:       errText,
		Syncable:    syncable,
		StartedAt:   runStarted,
		CompletedAt: now,
		TokensIn:    attempt.TokensIn,
		TokensOut:   attempt.TokensOut,
	}
	if err := o.timeline.UpsertNode(ctx, node); err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "timeline_node_upsert_failed", "node_id", node.NodeID, "err", err)
	}
}

func (o *Orchestrator) shouldSuppressLegacyComment() bool {
	return o.suppressLinearLegacyComments && tracker.IsLinearTracker(o.tracker)
}

func runStatusFromNodeStatus(status string) string {
	switch status {
	case timeline.NodeStatusSucceeded:
		return timeline.NodeStatusSucceeded
	case timeline.NodeStatusFailed:
		return timeline.NodeStatusFailed
	case timeline.NodeStatusNeedsReview:
		return timeline.NodeStatusNeedsReview
	case timeline.NodeStatusRetryQueued:
		return timeline.NodeStatusRetryQueued
	default:
		return timeline.NodeStatusStarted
	}
}

func runFinishedAt(status string, ts time.Time) time.Time {
	switch status {
	case timeline.NodeStatusSucceeded, timeline.NodeStatusFailed, timeline.NodeStatusNeedsReview:
		return ts
	default:
		return time.Time{}
	}
}

func timelineStatusForPhase(phase types.RunPhase) (suffix string, status string, title string) {
	switch phase {
	case types.Succeeded:
		return "complete", timeline.NodeStatusSucceeded, "Agent run completed"
	case types.CanceledByReconciliation:
		return "failed", timeline.NodeStatusFailed, "Agent run canceled"
	default:
		return "failed", timeline.NodeStatusFailed, "Agent run failed"
	}
}

func preAgentTimelineAttempt(issue types.Issue, attempt int, phase types.RunPhase, cause error) types.RunAttempt {
	return types.RunAttempt{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		Attempt:         attempt,
		Phase:           phase,
		StartTime:       time.Now(),
		Error:           fmt.Sprint(cause),
	}
}
