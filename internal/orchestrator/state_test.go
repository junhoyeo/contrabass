package orchestrator

import (
	"fmt"
	"testing"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type issueStateTransition struct {
	from types.IssueState
	to   types.IssueState
}

func TestTransitionIssueState_AllTransitions(t *testing.T) {
	issueStates := []types.IssueState{
		types.Unclaimed,
		types.Claimed,
		types.Running,
		types.RetryQueued,
		types.Released,
	}

	validTransitions := map[issueStateTransition]struct{}{
		{from: types.Unclaimed, to: types.Claimed}:    {},
		{from: types.Claimed, to: types.Running}:      {},
		{from: types.Running, to: types.RetryQueued}:  {},
		{from: types.RetryQueued, to: types.Claimed}:  {},
		{from: types.Unclaimed, to: types.Released}:   {},
		{from: types.Claimed, to: types.Released}:     {},
		{from: types.Running, to: types.Released}:     {},
		{from: types.RetryQueued, to: types.Released}: {},
		{from: types.Released, to: types.Released}:    {},
	}

	for _, from := range issueStates {
		from := from
		for _, to := range issueStates {
			to := to
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				err := TransitionIssueState(from, to)
				_, isValid := validTransitions[issueStateTransition{from: from, to: to}]

				if isValid {
					require.NoError(t, err)
					return
				}

				require.Error(t, err)
				var invalidTransitionErr *InvalidTransitionError
				require.ErrorAs(t, err, &invalidTransitionErr)
				assert.Equal(t, from, invalidTransitionErr.From)
				assert.Equal(t, to, invalidTransitionErr.To)
			})
		}
	}
}

type runPhaseTransition struct {
	from types.RunPhase
	to   types.RunPhase
}

func TestTransitionRunPhase_AllTransitions(t *testing.T) {
	runPhases := []types.RunPhase{
		types.PreparingWorkspace,
		types.BuildingPrompt,
		types.LaunchingAgentProcess,
		types.InitializingSession,
		types.StreamingTurn,
		types.Finishing,
		types.Succeeded,
		types.Failed,
		types.TimedOut,
		types.Stalled,
		types.CanceledByReconciliation,
	}

	activePhases := []types.RunPhase{
		types.PreparingWorkspace,
		types.BuildingPrompt,
		types.LaunchingAgentProcess,
		types.InitializingSession,
		types.StreamingTurn,
		types.Finishing,
	}

	failurePhases := []types.RunPhase{
		types.Failed,
		types.TimedOut,
		types.Stalled,
		types.CanceledByReconciliation,
	}

	validTransitions := map[runPhaseTransition]struct{}{
		{from: types.PreparingWorkspace, to: types.BuildingPrompt}:         {},
		{from: types.BuildingPrompt, to: types.LaunchingAgentProcess}:      {},
		{from: types.LaunchingAgentProcess, to: types.InitializingSession}: {},
		{from: types.InitializingSession, to: types.StreamingTurn}:         {},
		{from: types.StreamingTurn, to: types.Finishing}:                   {},
		{from: types.Finishing, to: types.Succeeded}:                       {},
	}

	for _, from := range activePhases {
		for _, to := range failurePhases {
			validTransitions[runPhaseTransition{from: from, to: to}] = struct{}{}
		}
	}

	for _, from := range runPhases {
		from := from
		for _, to := range runPhases {
			to := to
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				err := TransitionRunPhase(from, to)
				_, isValid := validTransitions[runPhaseTransition{from: from, to: to}]

				if isValid {
					require.NoError(t, err)
					return
				}

				require.Error(t, err)
				var invalidTransitionErr *InvalidTransitionError
				require.ErrorAs(t, err, &invalidTransitionErr)
				assert.Equal(t, from, invalidTransitionErr.From)
				assert.Equal(t, to, invalidTransitionErr.To)
			})
		}
	}
}

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		maxMs   int
	}{
		{name: "continuation_retry_uses_fixed_delay", attempt: 0, maxMs: 300_000},
		{name: "attempt_1_uses_base_backoff_with_jitter", attempt: 1, maxMs: 300_000},
		{name: "attempt_2_uses_exponential_backoff_with_jitter", attempt: 2, maxMs: 300_000},
		{name: "attempt_3_uses_exponential_backoff_with_jitter", attempt: 3, maxMs: 300_000},
		{name: "backoff_caps_at_max", attempt: 10, maxMs: 300_000},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			delayMs := CalculateBackoff(tt.attempt, tt.maxMs)

			if tt.attempt <= 0 {
				assert.Equal(t, 1_000, delayMs)
				return
			}

			baseDelay := expectedFailureBackoff(tt.attempt, tt.maxMs)
			jitterRange := baseDelay / 10
			minDelay := baseDelay - jitterRange
			maxDelay := baseDelay + jitterRange
			if maxDelay > tt.maxMs {
				maxDelay = tt.maxMs
			}

			assert.GreaterOrEqual(t, delayMs, minDelay)
			assert.LessOrEqual(t, delayMs, maxDelay)
			assert.Equal(t, delayMs, CalculateBackoff(tt.attempt, tt.maxMs), "backoff should be deterministic")
		})
	}
}

func TestCheckBoundedConcurrency(t *testing.T) {
	tests := []struct {
		name    string
		running int
		max     int
		want    bool
	}{
		{name: "below_limit_accepts_work", running: 2, max: 3, want: true},
		{name: "at_limit_rejects_work", running: 3, max: 3, want: false},
		{name: "above_limit_rejects_work", running: 4, max: 3, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CheckBoundedConcurrency(tt.running, tt.max))
		})
	}
}

func TestDetectStall_Boundaries(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		lastEventTime  time.Time
		stallTimeoutMs int
		want           bool
	}{
		{name: "before_timeout", lastEventTime: now.Add(-9 * time.Second), stallTimeoutMs: 10_000, want: false},
		{name: "at_timeout", lastEventTime: now.Add(-10 * time.Second), stallTimeoutMs: 10_000, want: false},
		{name: "after_timeout", lastEventTime: now.Add(-10*time.Second - 1*time.Millisecond), stallTimeoutMs: 10_000, want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, detectStallAt(now, tt.lastEventTime, tt.stallTimeoutMs))
		})
	}
}

func expectedFailureBackoff(attempt int, maxMs int) int {
	if attempt <= 0 {
		return 0
	}

	delay := 10_000
	for step := 1; step < attempt; step++ {
		if delay >= maxMs {
			return maxMs
		}
		if delay > maxMs/2 {
			delay = maxMs
			break
		}
		delay *= 2
	}

	if delay > maxMs {
		return maxMs
	}

	return delay
}
