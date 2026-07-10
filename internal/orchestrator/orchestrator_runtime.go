package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/types"
)

func (o *Orchestrator) handleRunSignal(ctx context.Context, signal runSignal) {
	if signal.event != nil {
		o.handleAgentEvent(signal.issueID, *signal.event)
	}
	if signal.done {
		o.completeRun(ctx, signal.issueID, signal.err)
	}
}

func (o *Orchestrator) handleAgentEvent(issueID string, event types.AgentEvent) {
	tokensIn, tokensOut := parseUsageTokens(event.Data)

	o.mu.Lock()
	entry, ok := o.running[issueID]
	if !ok {
		o.mu.Unlock()
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	entry.lastEventAt = event.Timestamp
	entry.lastHeartbeatAt = event.Timestamp
	entry.attempt.LastEvent = event.Type
	if !agent.IsHeartbeatEvent(event.Type) {
		entry.lastActivityAt = event.Timestamp
		entry.lastActivityKind = event.Type
	}

	if entry.attempt.Phase == types.InitializingSession {
		if err := TransitionRunPhase(entry.attempt.Phase, types.StreamingTurn); err == nil {
			entry.attempt.Phase = types.StreamingTurn
		}
	}

	if tokensIn > entry.attempt.TokensIn {
		delta := tokensIn - entry.attempt.TokensIn
		entry.attempt.TokensIn = tokensIn
		o.stats.TotalTokensIn += delta
	}
	if tokensOut > entry.attempt.TokensOut {
		delta := tokensOut - entry.attempt.TokensOut
		entry.attempt.TokensOut = tokensOut
		o.stats.TotalTokensOut += delta
	}

	if isFailureAgentEvent(event.Type) && isActiveRunPhase(entry.attempt.Phase) {
		if err := TransitionRunPhase(entry.attempt.Phase, types.Failed); err == nil {
			entry.attempt.Phase = types.Failed
		}
		if message := extractEventError(event.Data); message != "" {
			entry.attempt.Error = message
		}
	}

	o.mu.Unlock()

	logging.LogAgentEvent(o.logger, issueID, event.Type)
}

func (o *Orchestrator) completeRun(ctx context.Context, issueID string, doneErr error) {
	o.mu.Lock()
	entry, ok := o.running[issueID]
	if !ok {
		o.mu.Unlock()
		return
	}
	delete(o.running, issueID)
	o.stats.Running = len(o.running)
	eventTimestamp := time.Now()
	o.mu.Unlock()

	defer entry.cancel()

	finalAttempt := entry.attempt
	successSignal := completionSignalFromEvent(finalAttempt.LastEvent)
	phaseMessage := finalAttempt.Error
	if phaseMessage == "" {
		phaseMessage = successSignal
	}
	finalAttempt.Phase, finalAttempt.Error = resolveFinalPhase(finalAttempt.Phase, phaseMessage, doneErr)
	if successSignal != "" && finalAttempt.Error == successSignal {
		finalAttempt.Error = ""
	}

	o.emitEvent(OrchestratorEvent{
		Type:      EventAgentFinished,
		IssueID:   issueID,
		Timestamp: eventTimestamp,
		Data: AgentFinished{
			Attempt:   finalAttempt.Attempt,
			Phase:     finalAttempt.Phase,
			TokensIn:  finalAttempt.TokensIn,
			TokensOut: finalAttempt.TokensOut,
			Error:     finalAttempt.Error,
		},
	})

	if finalAttempt.Phase == types.Succeeded {
		advanced, reason, err := verifyBranchAdvancedWithTimeout(
			ctx,
			finalAttempt.WorkspacePath,
			entry.issue.BranchName,
			finalAttempt.ClaimHeadSha,
			o.runtimePolicy().gitCommandTimeout,
		)
		switch {
		case advanced && reason == "":
		case !advanced && reason == "branch_unchanged":
			logging.LogIssueEvent(o.logger, issueID,
				"success_unverified_branch_unchanged",
				"attempt", finalAttempt.Attempt,
				"branch", entry.issue.BranchName,
				"head", finalAttempt.ClaimHeadSha,
			)
			o.pauseUnverifiedSuccess(ctx, entry, finalAttempt,
				"success_unverified_branch_unchanged", nil)
			return
		case advanced && reason == "git_error":
			// Persistent git failure (e.g. workspace was never a real
			// worktree) — fail close to prevent rubber-stamped runs.
			// See harden-verify-success-gate Decision 1.
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			logging.LogIssueEvent(o.logger, issueID,
				"success_unverified_workspace_invalid",
				"attempt", finalAttempt.Attempt,
				"branch", entry.issue.BranchName,
				"head", finalAttempt.ClaimHeadSha,
				"err", errText,
			)
			o.pauseUnverifiedSuccess(ctx, entry, finalAttempt,
				"success_unverified_workspace_invalid", err)
			return
		case advanced && reason == "no_claim_head":
			// Empty ClaimHeadSha — claim-time SHA capture failed.
			// Keep fail-open to preserve retry recovery after restarts.
			o.logger.Warn("verifier_skipped",
				"issue_id", issueID, "reason", reason)
		default:
			// Unknown future reason — keep fail-open with explicit warn.
			if err != nil {
				o.logger.Warn("verifier_skipped",
					"issue_id", issueID, "reason", reason, "err", err)
			} else {
				o.logger.Warn("verifier_skipped",
					"issue_id", issueID, "reason", reason)
			}
		}
	}

	nodeSuffix, nodeStatus, nodeTitle := timelineStatusForPhase(finalAttempt.Phase)
	o.recordTimelineNode(ctx, entry.issue, finalAttempt,
		nodeSuffix, nodeStatus, nodeTitle, "Agent process reached a durable terminal state.", finalAttempt.Error, true)

	if err := o.workspace.Cleanup(ctx, issueID); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "workspace_cleanup_failed", "stage", "complete_run", "err", err)
	}

	// Post completion comment (best-effort)
	commentBody := fmt.Sprintf(
		"Agent run completed: phase=%s attempt=%d tokens_in=%d tokens_out=%d",
		finalAttempt.Phase.String(),
		finalAttempt.Attempt,
		finalAttempt.TokensIn,
		finalAttempt.TokensOut,
	)
	if finalAttempt.Error != "" {
		commentBody += fmt.Sprintf(" error=%q", finalAttempt.Error)
	}
	if o.shouldSuppressLegacyComment() {
		logging.LogIssueEvent(o.logger, issueID, "legacy_comment_suppressed", "reason", "linear_sync_enabled")
	} else if err := o.tracker.PostComment(ctx, issueID, commentBody); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "post_comment_failed", "err", err)
	}

	if finalAttempt.Phase == types.Succeeded {
		o.releaseIssue(ctx, issueID, types.Running, finalAttempt.Attempt)
		logging.LogAgentEvent(o.logger, issueID, "finished", "status", finalAttempt.Phase.String())
		return
	}

	logging.LogAgentEvent(
		o.logger,
		issueID,
		"finished",
		"status", finalAttempt.Phase.String(),
		"err", finalAttempt.Error,
	)

	o.enqueueBackoffFromRunResult(ctx, entry.issue, finalAttempt)
}

func resolveFinalPhase(phase types.RunPhase, message string, doneErr error) (types.RunPhase, string) {
	finalPhase := phase
	finalMessage := message

	if doneErr != nil {
		if errors.Is(doneErr, context.Canceled) {
			if isActiveRunPhase(finalPhase) {
				if err := TransitionRunPhase(finalPhase, types.CanceledByReconciliation); err == nil {
					finalPhase = types.CanceledByReconciliation
				}
			}
			if finalMessage == "" {
				finalMessage = doneErr.Error()
			}
			return finalPhase, finalMessage
		}

		if isActiveRunPhase(finalPhase) {
			if err := TransitionRunPhase(finalPhase, types.Failed); err == nil {
				finalPhase = types.Failed
			}
		}
		if finalMessage == "" {
			finalMessage = doneErr.Error()
		}
		return finalPhase, finalMessage
	}

	if isFailureRunPhase(finalPhase) {
		return finalPhase, finalMessage
	}

	if isActiveRunPhase(finalPhase) {
		if canCompleteWithoutEvents(finalPhase) {
			finalPhase = types.Succeeded
		} else if err := TransitionRunPhase(finalPhase, types.Finishing); err == nil {
			finalPhase = types.Finishing
		}
	}
	if finalPhase == types.Finishing {
		if hasExplicitSuccessSignal(finalMessage) {
			if err := TransitionRunPhase(finalPhase, types.Succeeded); err == nil {
				finalPhase = types.Succeeded
			}
		} else if finalMessage == "" {
			finalMessage = "missing explicit success signal"
		}
	}

	return finalPhase, finalMessage
}

func completionSignalFromEvent(eventType string) string {
	normalized := strings.TrimSpace(strings.ToLower(eventType))
	if hasExplicitSuccessSignal(normalized) {
		return normalized
	}
	return ""
}

func hasExplicitSuccessSignal(message string) bool {
	normalized := strings.TrimSpace(strings.ToLower(message))
	if normalized == "" {
		return false
	}

	switch normalized {
	case "turn/completed", "item/completed", "task/completed", "session.status":
		return true
	}

	return strings.Contains(normalized, "completed") && strings.Contains(normalized, "success")
}

func (o *Orchestrator) enqueueBackoffFromRunResult(ctx context.Context, issue types.Issue, attempt types.RunAttempt) {
	if issueTransitionErr := TransitionIssueState(types.Running, types.RetryQueued); issueTransitionErr == nil {
		if updateErr := o.tracker.UpdateIssueState(ctx, issue.ID, types.RetryQueued); updateErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "update_retry_queued_failed", "err", updateErr)
		}
	}

	releaseTimestamp := time.Now()
	if releaseErr := o.tracker.ReleaseIssue(ctx, issue.ID); releaseErr != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "release_failed", "err", releaseErr)
	} else {
		o.emitIssueReleased(issue.ID, attempt.Attempt, releaseTimestamp)
	}

	delayMs := o.backoffDelayMs(issue.ID, attempt.Attempt)
	retryAt := time.Now().Add(time.Duration(delayMs) * time.Millisecond)
	nextAttempt := attempt.Attempt + 1

	entry := types.BackoffEntry{
		IssueID: issue.ID,
		Attempt: nextAttempt,
		RetryAt: retryAt,
		Error:   attempt.Error,
	}

	o.mu.Lock()
	o.backoff = upsertBackoff(o.backoff, entry)
	o.putIssueCacheLocked(issue.ID, issue)
	eventTimestamp := time.Now()
	o.mu.Unlock()

	o.emitEvent(OrchestratorEvent{
		Type:      EventBackoffEnqueued,
		IssueID:   issue.ID,
		Timestamp: eventTimestamp,
		Data: BackoffEnqueued{
			Attempt: nextAttempt,
			RetryAt: retryAt,
			Error:   attempt.Error,
		},
	})

	o.recordTimelineNode(ctx, issue, attempt,
		"retry-queued", "retry_queued", "Retry queued", fmt.Sprintf("Contrabass scheduled retry attempt %d.", nextAttempt), attempt.Error, true)

	logging.LogOrchestratorEvent(
		o.logger,
		"backoff_enqueued",
		"issue_id", issue.ID,
		"attempt", nextAttempt,
		"retry_at", retryAt,
		"err", attempt.Error,
	)
}

func (o *Orchestrator) enqueueBackoffFromRunning(ctx context.Context, issue types.Issue, attempt types.RunAttempt, startErr error) {
	attempt.Error = startErr.Error()
	if isActiveRunPhase(attempt.Phase) {
		if err := TransitionRunPhase(attempt.Phase, types.Failed); err == nil {
			attempt.Phase = types.Failed
		}
	}
	o.enqueueBackoffFromRunResult(ctx, issue, attempt)
}

func (o *Orchestrator) releaseClaimAndQueueContinuation(ctx context.Context, issueID string, attempt int, cause error) {
	releaseTimestamp := time.Now()

	if err := o.tracker.UpdateIssueState(ctx, issueID, types.Released); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "update_released_failed", "err", err)
	}
	if err := o.tracker.ReleaseIssue(ctx, issueID); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "release_failed", "err", err)
	}
	o.enqueueContinuation(issueID, attempt, cause.Error())
	o.emitIssueReleased(issueID, attempt, releaseTimestamp)
}

func (o *Orchestrator) pauseUnverifiedSuccess(
	ctx context.Context,
	entry *runEntry,
	attempt types.RunAttempt,
	cause string,
	verifyErr error,
) {
	issueID := entry.issue.ID
	if err := o.workspace.Cleanup(ctx, issueID); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "workspace_cleanup_failed", "stage", "unverified_success", "err", err)
	}
	if err := o.tracker.UpdateIssueState(ctx, issueID, types.Running); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "update_running_failed", "stage", "unverified_success", "err", err)
	}

	commentBody := fmt.Sprintf(
		"Agent reported success, but Contrabass could not verify branch changes (%s). Leaving the issue in progress for manual review; no retry was scheduled.",
		cause,
	)
	if verifyErr != nil {
		commentBody += fmt.Sprintf(" error=%q", verifyErr.Error())
	}
	o.recordTimelineNode(ctx, entry.issue, attempt,
		"needs-review", "needs_review", "Manual review needed", commentBody, cause, true)
	if o.shouldSuppressLegacyComment() {
		logging.LogIssueEvent(o.logger, issueID, "legacy_comment_suppressed", "stage", "unverified_success", "reason", "linear_sync_enabled")
	} else if err := o.tracker.PostComment(ctx, issueID, commentBody); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "post_comment_failed", "stage", "unverified_success", "err", err)
	}

	cached := entry.issue
	cached.State = types.Running
	o.mu.Lock()
	o.paused[issueID] = cause
	o.putIssueCacheLocked(issueID, cached)
	o.mu.Unlock()

	logging.LogIssueEvent(o.logger, issueID, "success_unverified_paused",
		"attempt", attempt.Attempt, "cause", cause)
	logging.LogAgentEvent(o.logger, issueID, "finished",
		"status", attempt.Phase.String(), "err", cause)
}

func (o *Orchestrator) enqueueContinuation(issueID string, attempt int, message string) {
	delayMs := o.backoffDelayMs(issueID, 0)
	retryAt := time.Now().Add(time.Duration(delayMs) * time.Millisecond)

	entry := types.BackoffEntry{
		IssueID: issueID,
		Attempt: attempt,
		RetryAt: retryAt,
		Error:   message,
	}

	o.mu.Lock()
	o.backoff = upsertBackoff(o.backoff, entry)
	eventTimestamp := time.Now()
	o.mu.Unlock()

	o.emitEvent(OrchestratorEvent{
		Type:      EventBackoffEnqueued,
		IssueID:   issueID,
		Timestamp: eventTimestamp,
		Data: BackoffEnqueued{
			Attempt: attempt,
			RetryAt: retryAt,
			Error:   message,
		},
	})
}

func upsertBackoff(entries []types.BackoffEntry, next types.BackoffEntry) []types.BackoffEntry {
	for i := range entries {
		if entries[i].IssueID == next.IssueID {
			entries[i] = next
			return entries
		}
	}
	return append(entries, next)
}

func (o *Orchestrator) backoffDelayMs(issueID string, attempt int) int {
	cfg := o.currentConfig()
	return calculateBackoffWithPolicy(
		issueID,
		attempt,
		cfg.MaxRetryBackoffMs(),
		runtimePolicyFromConfig(cfg).backoff,
	)
}

func (o *Orchestrator) requeueBackoff(entry types.BackoffEntry) {
	o.mu.Lock()
	o.backoff = append(o.backoff, entry)
	o.mu.Unlock()
}

func (o *Orchestrator) reconcileRunning(ctx context.Context, cfg *config.WorkflowConfig) {
	timeout := time.Duration(cfg.AgentTimeoutMs()) * time.Millisecond
	if timeout <= 0 {
		return
	}

	now := time.Now()
	orphaned := make([]string, 0)
	forceRemoved := make([]string, 0)

	o.mu.Lock()
	for issueID, entry := range o.running {
		if entry == nil || entry.process == nil || entry.process.Done == nil {
			delete(o.running, issueID)
			forceRemoved = append(forceRemoved, issueID)
			continue
		}
		if now.Sub(entry.attempt.StartTime) > timeout && isActiveRunPhase(entry.attempt.Phase) {
			if err := TransitionRunPhase(entry.attempt.Phase, types.TimedOut); err == nil {
				entry.attempt.Phase = types.TimedOut
			}
			entry.attempt.Error = "run timed out"
			orphaned = append(orphaned, issueID)
		}
	}
	o.stats.Running = len(o.running)
	o.mu.Unlock()

	for _, issueID := range forceRemoved {
		logging.LogOrchestratorEvent(
			o.logger,
			"run_force_removed",
			"issue_id", issueID,
			"reason", "missing_process_or_done",
		)
	}

	for _, issueID := range orphaned {
		o.stopRun(ctx, issueID)
	}
}

func (o *Orchestrator) detectStalledRuns(ctx context.Context, cfg *config.WorkflowConfig) {
	stalled := make([]string, 0)

	o.mu.Lock()
	for issueID, entry := range o.running {
		if entry == nil || !isActiveRunPhase(entry.attempt.Phase) {
			continue
		}
		if detectStall(entry.lastEventAt, cfg.StallTimeoutMs()) {
			if err := TransitionRunPhase(entry.attempt.Phase, types.Stalled); err == nil {
				entry.attempt.Phase = types.Stalled
			}
			entry.attempt.Error = "run stalled"
			stalled = append(stalled, issueID)
		}
	}
	o.mu.Unlock()

	for _, issueID := range stalled {
		o.stopRun(ctx, issueID)
	}
}

func (o *Orchestrator) stopRun(_ context.Context, issueID string) {
	o.mu.Lock()
	entry, ok := o.running[issueID]
	o.mu.Unlock()
	if !ok || entry == nil {
		return
	}

	entry.cancel()
	if err := o.agent.Stop(entry.process); err != nil {
		logging.LogAgentEvent(o.logger, issueID, "stop_failed", "err", err)
	}

	if entry.process != nil && entry.process.Done != nil {
		graceTimeout := o.runtimePolicy().stopGraceTimeout
		graceTimer := time.NewTimer(graceTimeout)
		select {
		case _, ok := <-entry.process.Done:
			if !ok {
				logging.LogOrchestratorEvent(o.logger, "run_stop_done_closed", "issue_id", issueID)
			}
		case <-graceTimer.C:
			logging.LogOrchestratorEvent(
				o.logger,
				"run_stop_timeout",
				"issue_id", issueID,
				"grace_timeout", graceTimeout.String(),
			)
		}
		if !graceTimer.Stop() {
			select {
			case <-graceTimer.C:
			default:
			}
		}
	}

	o.mu.Lock()
	current, stillRunning := o.running[issueID]
	if stillRunning && current == entry {
		delete(o.running, issueID)
		o.stats.Running = len(o.running)
	}
	o.mu.Unlock()

	logging.LogOrchestratorEvent(o.logger, "run_stopped", "issue_id", issueID, "cleaned_up", true)
}

func (o *Orchestrator) releaseIssue(ctx context.Context, issueID string, from types.IssueState, attempt int) {
	releaseTimestamp := time.Now()

	if issueTransitionErr := TransitionIssueState(from, types.Released); issueTransitionErr == nil {
		if updateErr := o.tracker.UpdateIssueState(ctx, issueID, types.Released); updateErr != nil {
			logging.LogIssueEvent(o.logger, issueID, "update_released_failed", "err", updateErr)
		}
	}

	if releaseErr := o.tracker.ReleaseIssue(ctx, issueID); releaseErr != nil {
		logging.LogIssueEvent(o.logger, issueID, "release_failed", "err", releaseErr)
		return
	}

	o.emitIssueReleased(issueID, attempt, releaseTimestamp)
}

func (o *Orchestrator) emitIssueReleased(issueID string, attempt int, timestamp time.Time) {
	o.emitEvent(OrchestratorEvent{
		Type:      EventIssueReleased,
		IssueID:   issueID,
		Data:      IssueReleased{Attempt: attempt},
		Timestamp: timestamp,
	})
}

func (o *Orchestrator) emitStatusUpdate() {
	o.mu.Lock()
	stats := o.stats
	backoffQueue := len(o.backoff)
	o.mu.Unlock()
	cfg := o.currentConfig()
	modelName, _ := cfg.Model()
	projectURL := cfg.TrackerProjectURL()
	trackerType := cfg.TrackerType()
	trackerScope := cfg.TrackerProjectURL()
	if trackerType == "internal" || trackerType == "local" {
		trackerScope = cfg.LocalBoardDir()
	}
	o.emitEvent(OrchestratorEvent{
		Type: EventStatusUpdate,
		Data: StatusUpdate{
			Stats:        stats,
			BackoffQueue: backoffQueue,
			ModelName:    modelName,
			ProjectURL:   projectURL,
			TrackerType:  trackerType,
			TrackerScope: trackerScope,
		},
	})
}

func (o *Orchestrator) emitEvent(event OrchestratorEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	o.mu.Lock()
	if o.eventsClosed.Load() {
		o.mu.Unlock()
		return
	}

	select {
	case o.events <- event:
		o.mu.Unlock()
	default:
		o.mu.Unlock()
		logging.LogOrchestratorEvent(
			o.logger,
			"event_dropped",
			"type", event.Type.String(),
			"issue_id", event.IssueID,
		)
	}
}

func (o *Orchestrator) canDispatch(maxAgents int) bool {
	o.mu.Lock()
	running := len(o.running)
	o.mu.Unlock()

	return checkBoundedConcurrency(running, maxAgents)
}

func (o *Orchestrator) isManagedIssue(issueID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	if _, ok := o.running[issueID]; ok {
		return true
	}
	if _, ok := o.paused[issueID]; ok {
		return true
	}
	for _, backoffEntry := range o.backoff {
		if backoffEntry.IssueID == issueID {
			return true
		}
	}

	return false
}

func (o *Orchestrator) gracefulShutdown(ctx context.Context) error {
	var cleanupAllErr error

	o.shutdownOnce.Do(func() {
		o.mu.Lock()
		runs := make([]*runEntry, 0, len(o.running))
		for _, run := range o.running {
			runs = append(runs, run)
		}
		clear(o.running)
		o.stats.Running = 0
		o.mu.Unlock()

		for _, run := range runs {
			if run == nil {
				continue
			}
			if run.cancel != nil {
				run.cancel()
			}
			if err := o.agent.Stop(run.process); err != nil {
				logging.LogIssueEvent(o.logger, run.issue.ID, "shutdown_stop_failed", "err", err)
			}
			if err := o.workspace.Cleanup(ctx, run.issue.ID); err != nil {
				logging.LogIssueEvent(o.logger, run.issue.ID, "shutdown_cleanup_failed", "err", err)
			}
			if err := o.tracker.UpdateIssueState(ctx, run.issue.ID, types.Released); err != nil {
				logging.LogIssueEvent(o.logger, run.issue.ID, "shutdown_update_released_failed", "err", err)
			}
			if err := o.tracker.ReleaseIssue(ctx, run.issue.ID); err != nil {
				logging.LogIssueEvent(o.logger, run.issue.ID, "shutdown_release_failed", "err", err)
			}
		}

		if err := o.workspace.CleanupAll(ctx); err != nil {
			cleanupAllErr = err
			logging.LogOrchestratorEvent(o.logger, "cleanup_all_failed", "err", err)
		}

		logging.LogOrchestratorEvent(o.logger, "graceful_shutdown_completed", "released_runs", len(runs))
	})

	return cleanupAllErr
}

func (o *Orchestrator) currentConfig() *config.WorkflowConfig {
	if o.config == nil {
		return &config.WorkflowConfig{}
	}
	cfg := o.config.GetConfig()
	if cfg == nil {
		return &config.WorkflowConfig{}
	}
	return cfg
}

func parseUsageTokens(data map[string]interface{}) (int64, int64) {
	if data == nil {
		return 0, 0
	}

	// codex 0.128+ shape: thread/tokenUsage/updated event
	//   params.tokenUsage.total.{inputTokens, outputTokens, ...}   cumulative across turns
	//   params.tokenUsage.last.{inputTokens, outputTokens, ...}    this turn only
	//   params.tokenUsage.modelContextWindow                       informational
	// Use total to feed the orchestrator's cumulative delta accounting.
	if tu, ok := data["tokenUsage"].(map[string]interface{}); ok {
		// Modern (codex 0.128 verified): total is itself a map of counters.
		if total, ok := tu["total"].(map[string]interface{}); ok {
			in := firstInt64(total, "inputTokens", "input_tokens", "prompt_tokens")
			out := firstInt64(total, "outputTokens", "output_tokens", "completion_tokens")
			if in != 0 || out != 0 {
				return in, out
			}
		}
		// Legacy hypothesis (per older docs): tokenUsage.context.{inputTokens, outputTokens}.
		if ctx, ok := tu["context"].(map[string]interface{}); ok {
			in := firstInt64(ctx, "inputTokens", "input_tokens", "prompt_tokens")
			out := firstInt64(ctx, "outputTokens", "output_tokens", "completion_tokens")
			if in != 0 || out != 0 {
				return in, out
			}
		}
		// Very old hypothesis: tokenUsage.total is itself an integer.
		if total := firstInt64(tu, "total"); total != 0 {
			return 0, total
		}
	}

	// Legacy + omx shape: data["usage"]
	rawUsage, ok := data["usage"]
	if !ok {
		return 0, 0
	}

	usage, ok := rawUsage.(map[string]interface{})
	if !ok {
		return 0, 0
	}

	tokensIn := firstInt64(usage, "prompt_tokens", "input_tokens")
	tokensOut := firstInt64(usage, "completion_tokens", "output_tokens")
	if tokensIn == 0 && tokensOut == 0 {
		tokensOut = firstInt64(usage, "total_tokens")
	}

	return tokensIn, tokensOut
}

func firstInt64(values map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		parsed, err := parseInt64(value)
		if err == nil {
			return parsed
		}
	}

	return 0
}

func parseInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func isFailureAgentEvent(eventType string) bool {
	switch eventType {
	case "turn/failed", "turn/cancelled", "turn/canceled":
		return true
	default:
		return false
	}
}

func extractEventError(data map[string]interface{}) string {
	if data == nil {
		return ""
	}

	for _, key := range []string{"error", "message", "reason"} {
		raw, ok := data[key]
		if !ok {
			continue
		}
		if text, ok := raw.(string); ok {
			return text
		}
	}

	return ""
}
