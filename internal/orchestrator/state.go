package orchestrator

import (
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const (
	continuationBackoffMs = 1_000
	failureBackoffBaseMs  = 10_000
)

type InvalidTransitionError struct {
	From any
	To   any
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid transition: %v -> %v", e.From, e.To)
}

func TransitionIssueState(current, target types.IssueState) error {
	if target == types.Released {
		return nil
	}

	switch current {
	case types.Unclaimed:
		if target == types.Claimed {
			return nil
		}
	case types.Claimed:
		if target == types.Running {
			return nil
		}
	case types.Running:
		if target == types.RetryQueued {
			return nil
		}
	case types.RetryQueued:
		if target == types.Claimed {
			return nil
		}
	}

	return &InvalidTransitionError{From: current, To: target}
}

func TransitionRunPhase(current, target types.RunPhase) error {
	if isActiveRunPhase(current) && isFailureRunPhase(target) {
		return nil
	}

	switch current {
	case types.PreparingWorkspace:
		if target == types.BuildingPrompt {
			return nil
		}
	case types.BuildingPrompt:
		if target == types.LaunchingAgentProcess {
			return nil
		}
	case types.LaunchingAgentProcess:
		if target == types.InitializingSession {
			return nil
		}
	case types.InitializingSession:
		if target == types.StreamingTurn {
			return nil
		}
	case types.StreamingTurn:
		if target == types.Finishing {
			return nil
		}
	case types.Finishing:
		if target == types.Succeeded {
			return nil
		}
	}

	return &InvalidTransitionError{From: current, To: target}
}

func CalculateBackoff(issueID string, attempt int, maxMs int) (delayMs int) {
	if maxMs <= 0 {
		return 0
	}

	if attempt <= 0 {
		if continuationBackoffMs > maxMs {
			return maxMs
		}
		return continuationBackoffMs
	}

	baseDelay := calculateFailureBackoff(attempt, maxMs)
	jitterRange := baseDelay / 10
	if jitterRange <= 0 {
		return baseDelay
	}

	offset := deterministicJitterOffset(issueID, attempt, maxMs, jitterRange)
	delayMs = baseDelay + offset
	minBackoffMs := continuationBackoffMs
	if minBackoffMs <= 0 {
		minBackoffMs = 1_000
	}
	if minBackoffMs > maxMs {
		minBackoffMs = maxMs
	}
	if delayMs < minBackoffMs {
		delayMs = minBackoffMs
	}
	if delayMs > maxMs {
		return maxMs
	}

	return delayMs
}

func checkBoundedConcurrency(running, max int) bool {
	return running < max
}

func detectStall(lastEventTime time.Time, stallTimeoutMs int) bool {
	return detectStallAt(time.Now(), lastEventTime, stallTimeoutMs)
}

func detectStallAt(now, lastEventTime time.Time, stallTimeoutMs int) bool {
	if stallTimeoutMs <= 0 {
		return false
	}

	timeout := time.Duration(stallTimeoutMs) * time.Millisecond
	return now.Sub(lastEventTime) > timeout
}

func isActiveRunPhase(phase types.RunPhase) bool {
	switch phase {
	case types.PreparingWorkspace,
		types.BuildingPrompt,
		types.LaunchingAgentProcess,
		types.InitializingSession,
		types.StreamingTurn,
		types.Finishing:
		return true
	default:
		return false
	}
}

func isFailureRunPhase(phase types.RunPhase) bool {
	switch phase {
	case types.Failed,
		types.TimedOut,
		types.Stalled,
		types.CanceledByReconciliation:
		return true
	default:
		return false
	}
}

func canCompleteWithoutEvents(phase types.RunPhase) bool {
	return phase == types.InitializingSession
}

func deterministicJitterOffset(issueID string, attempt, maxMs, jitterRange int) int {
	span := (2 * jitterRange) + 1
	if span <= 0 {
		return 0
	}

	seed := deterministicBackoffSeed(issueID, attempt, maxMs)
	return int(seed%uint64(span)) - jitterRange
}

func deterministicBackoffSeed(issueID string, attempt, maxMs int) uint64 {
	const (
		offsetBasis = uint64(1469598103934665603)
		prime       = uint64(1099511628211)
	)

	seed := offsetBasis
	for i := 0; i < len(issueID); i++ {
		seed ^= uint64(issueID[i])
		seed *= prime
	}

	seed ^= uint64(uint32(attempt))
	seed *= prime
	seed ^= uint64(uint32(maxMs))
	seed *= prime

	return seed
}

func calculateFailureBackoff(attempt int, maxMs int) int {
	delay := failureBackoffBaseMs
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
