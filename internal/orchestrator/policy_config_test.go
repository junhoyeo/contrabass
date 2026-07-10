package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestNewOrchestrator_UsesConfiguredCapacityPolicy(t *testing.T) {
	t.Parallel()

	cfg := &config.WorkflowConfig{
		Orchestrator: config.OrchestratorConfig{
			EventBufferSize:     7,
			RunSignalBufferSize: 8,
			IssueCacheSize:      2,
		},
	}
	orch := NewOrchestrator(nil, nil, nil, &staticConfig{cfg: cfg}, nil)

	assert.Equal(t, 7, cap(orch.events))
	assert.Equal(t, 8, orch.runSignalBufferSize)
	orch.mu.Lock()
	orch.putIssueCacheLocked("1", types.Issue{ID: "1"})
	orch.putIssueCacheLocked("2", types.Issue{ID: "2"})
	orch.putIssueCacheLocked("3", types.Issue{ID: "3"})
	_, firstRetained := orch.issueCache["1"]
	orch.mu.Unlock()
	assert.False(t, firstRetained)
}

type sequencedConfigProvider struct {
	configs []*config.WorkflowConfig
	calls   int
}

func (p *sequencedConfigProvider) GetConfig() *config.WorkflowConfig {
	index := p.calls
	p.calls++
	if index >= len(p.configs) {
		index = len(p.configs) - 1
	}
	return p.configs[index]
}

func TestOrchestrator_BackoffUsesSingleConfigSnapshot(t *testing.T) {
	t.Parallel()

	zeroJitter := 0
	first := &config.WorkflowConfig{
		MaxRetryBackoffMsRaw: 5_000,
		Orchestrator: config.OrchestratorConfig{
			Backoff: config.OrchestratorBackoffConfig{
				ContinuationMs: 100,
				JitterPercent:  &zeroJitter,
			},
		},
	}
	second := &config.WorkflowConfig{
		MaxRetryBackoffMsRaw: 500,
		Orchestrator: config.OrchestratorConfig{
			Backoff: config.OrchestratorBackoffConfig{
				ContinuationMs: 5_000,
				JitterPercent:  &zeroJitter,
			},
		},
	}
	provider := &sequencedConfigProvider{configs: []*config.WorkflowConfig{first, second}}
	orch := NewOrchestrator(nil, nil, nil, nil, nil)
	orch.config = provider

	assert.Equal(t, 100, orch.backoffDelayMs("CB-1", 0))
	assert.Equal(t, 1, provider.calls)
}

func TestCalculateBackoffWithPolicy(t *testing.T) {
	t.Parallel()

	linear := backoffPolicy{
		strategy:       "linear",
		continuationMs: 250,
		failureBaseMs:  1000,
		multiplier:     3,
		jitterPercent:  0,
	}
	exponential := linear
	exponential.strategy = "exponential"

	assert.Equal(t, 250, calculateBackoffWithPolicy("CB-1", 0, 100_000, linear))
	assert.Equal(t, 3000, calculateBackoffWithPolicy("CB-1", 3, 100_000, linear))
	assert.Equal(t, 9000, calculateBackoffWithPolicy("CB-1", 3, 100_000, exponential))
}

func TestSnapshotPolicyOverridesStageAndETAThresholds(t *testing.T) {
	t.Parallel()

	policy := snapshotPolicy{
		stage: stagePolicy{
			testingAfter:                time.Second,
			reviewingAfter:              2 * time.Second,
			reviewingMaxTokensPerMinute: 100,
		},
		eta: etaPolicy{
			minElapsed:               time.Minute,
			minFilesPerMinute:        0.1,
			minTokensPerMinute:       10,
			mediumConfidenceAfter:    2 * time.Minute,
			highConfidenceAfter:      3 * time.Minute,
			highConfidenceMinStage:   3,
			estimatedFilesMultiplier: 1.1,
			minEstimatedFiles:        2,
			fallbackRemainingMinutes: 1,
			uncertaintyMultiplier:    1,
		},
	}
	now := time.Now()
	state := &agentStageState{LastDiffChange: now.Add(-3 * time.Second)}

	stage, step := classifyAgentStageWithPolicy(state, "tool_call", 0, 0, 50, now, policy.stage)
	assert.Equal(t, "Reviewing", stage)
	assert.Equal(t, 4, step)

	etaAt, confidence := estimateCompletionAtWithPolicy(
		now.Add(-4*time.Minute), now, 4, 100, 100, 4, 8, policy.eta,
	)
	require.NotEmpty(t, etaAt)
	assert.Equal(t, "high", confidence)
}

func TestOrchestrator_ShutdownConfigUsesWorkflowPolicy(t *testing.T) {
	t.Parallel()

	cfg := &config.WorkflowConfig{
		Orchestrator: config.OrchestratorConfig{
			Shutdown: config.OrchestratorShutdownConfig{
				DrainTimeoutMs:   11,
				CleanupTimeoutMs: 12,
				PollIntervalMs:   13,
			},
		},
	}
	orch := NewOrchestrator(nil, nil, nil, &staticConfig{cfg: cfg}, nil)

	shutdown := orch.ShutdownConfig()
	assert.Equal(t, 11*time.Millisecond, shutdown.DrainTimeout)
	assert.Equal(t, 12*time.Millisecond, shutdown.CleanupTimeout)
	assert.Equal(t, 13*time.Millisecond, shutdown.PollInterval)
}
