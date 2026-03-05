package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/logging"
	"github.com/junhoyeo/symphony-charm/internal/types"
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
	entry.attempt.LastEvent = event.Type

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
	o.mu.Unlock()

	defer entry.cancel()
	_ = o.workspace.Cleanup(ctx, issueID)

	finalAttempt := entry.attempt
	finalAttempt.Phase, finalAttempt.Error = resolveFinalPhase(finalAttempt.Phase, finalAttempt.Error, doneErr)

	o.emitEvent(OrchestratorEvent{
		Type:    EventAgentFinished,
		IssueID: issueID,
		Data: AgentFinished{
			Attempt:   finalAttempt.Attempt,
			Phase:     finalAttempt.Phase,
			TokensIn:  finalAttempt.TokensIn,
			TokensOut: finalAttempt.TokensOut,
			Error:     finalAttempt.Error,
		},
	})

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
		if err := TransitionRunPhase(finalPhase, types.Succeeded); err == nil {
			finalPhase = types.Succeeded
		}
	}

	return finalPhase, finalMessage
}

func (o *Orchestrator) enqueueBackoffFromRunResult(ctx context.Context, issue types.Issue, attempt types.RunAttempt) {
	if issueTransitionErr := TransitionIssueState(types.Running, types.RetryQueued); issueTransitionErr == nil {
		if updateErr := o.tracker.UpdateIssueState(ctx, issue.ID, types.RetryQueued); updateErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "update_retry_queued_failed", "err", updateErr)
		}
	}

	if releaseErr := o.tracker.ReleaseIssue(ctx, issue.ID); releaseErr != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "release_failed", "err", releaseErr)
	} else {
		o.emitEvent(OrchestratorEvent{
			Type:    EventIssueReleased,
			IssueID: issue.ID,
			Data:    IssueReleased{Attempt: attempt.Attempt},
		})
	}

	delayMs := CalculateBackoff(attempt.Attempt, o.currentConfig().MaxRetryBackoffMs())
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
	o.issueCache[issue.ID] = issue
	o.mu.Unlock()

	o.emitEvent(OrchestratorEvent{
		Type:    EventBackoffEnqueued,
		IssueID: issue.ID,
		Data: BackoffEnqueued{
			Attempt: nextAttempt,
			RetryAt: retryAt,
			Error:   attempt.Error,
		},
	})

	logging.LogOrchestratorEvent(
		o.logger,
		"backoff_enqueued",
		"issue_id", issue.ID,
		"attempt", nextAttempt,
		"retry_at", retryAt,
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
	_ = o.tracker.UpdateIssueState(ctx, issueID, types.Released)
	_ = o.tracker.ReleaseIssue(ctx, issueID)
	o.enqueueContinuation(issueID, attempt, cause.Error())
	o.emitEvent(OrchestratorEvent{
		Type:    EventIssueReleased,
		IssueID: issueID,
		Data:    IssueReleased{Attempt: attempt},
	})
}

func (o *Orchestrator) enqueueContinuation(issueID string, attempt int, message string) {
	delayMs := CalculateBackoff(0, o.currentConfig().MaxRetryBackoffMs())
	retryAt := time.Now().Add(time.Duration(delayMs) * time.Millisecond)

	entry := types.BackoffEntry{
		IssueID: issueID,
		Attempt: attempt,
		RetryAt: retryAt,
		Error:   message,
	}

	o.mu.Lock()
	o.backoff = upsertBackoff(o.backoff, entry)
	o.mu.Unlock()

	o.emitEvent(OrchestratorEvent{
		Type:    EventBackoffEnqueued,
		IssueID: issueID,
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
			if err := TransitionRunPhase(entry.attempt.Phase, types.CanceledByReconciliation); err == nil {
				entry.attempt.Phase = types.CanceledByReconciliation
			}
			entry.attempt.Error = "run exceeded timeout"
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
		graceTimer := time.NewTimer(5 * time.Second)
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
				"grace_timeout", "5s",
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
	if issueTransitionErr := TransitionIssueState(from, types.Released); issueTransitionErr == nil {
		if updateErr := o.tracker.UpdateIssueState(ctx, issueID, types.Released); updateErr != nil {
			logging.LogIssueEvent(o.logger, issueID, "update_released_failed", "err", updateErr)
		}
	}

	if releaseErr := o.tracker.ReleaseIssue(ctx, issueID); releaseErr != nil {
		logging.LogIssueEvent(o.logger, issueID, "release_failed", "err", releaseErr)
		return
	}

	o.emitEvent(OrchestratorEvent{
		Type:    EventIssueReleased,
		IssueID: issueID,
		Data:    IssueReleased{Attempt: attempt},
	})
}

func (o *Orchestrator) emitStatusUpdate() {
	o.mu.Lock()
	stats := o.stats
	backoffQueue := len(o.backoff)
	o.mu.Unlock()

	o.emitEvent(OrchestratorEvent{
		Type: EventStatusUpdate,
		Data: StatusUpdate{
			Stats:        stats,
			BackoffQueue: backoffQueue,
		},
	})
}

func (o *Orchestrator) emitEvent(event OrchestratorEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	select {
	case o.events <- event:
	default:
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
	for _, backoffEntry := range o.backoff {
		if backoffEntry.IssueID == issueID {
			return true
		}
	}

	return false
}

func (o *Orchestrator) shutdown(ctx context.Context) {
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
		run.cancel()
		_ = o.agent.Stop(run.process)
		_ = o.workspace.Cleanup(ctx, run.issue.ID)
		_ = o.tracker.UpdateIssueState(ctx, run.issue.ID, types.Released)
		_ = o.tracker.ReleaseIssue(ctx, run.issue.ID)
	}

	if err := o.workspace.CleanupAll(ctx); err != nil {
		logging.LogOrchestratorEvent(o.logger, "cleanup_all_failed", "err", err)
	}
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
