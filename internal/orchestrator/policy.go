package orchestrator

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
)

type runtimePolicy struct {
	eventBufferSize     int
	runSignalBufferSize int
	issueCacheSize      int
	runShutdownTimeout  time.Duration
	stopGraceTimeout    time.Duration
	gitCommandTimeout   time.Duration
	shutdown            ShutdownConfig
	backoff             backoffPolicy
	snapshot            snapshotPolicy
}

type backoffPolicy struct {
	strategy       string
	continuationMs int
	failureBaseMs  int
	multiplier     int
	jitterPercent  int
}

type snapshotPolicy struct {
	diffTimeout time.Duration
	stage       stagePolicy
	eta         etaPolicy
}

type stagePolicy struct {
	testingAfter                time.Duration
	reviewingAfter              time.Duration
	reviewingMaxTokensPerMinute float64
}

type etaPolicy struct {
	minElapsed               time.Duration
	minFilesPerMinute        float64
	minTokensPerMinute       float64
	mediumConfidenceAfter    time.Duration
	highConfidenceAfter      time.Duration
	highConfidenceMinStage   int
	estimatedFilesMultiplier float64
	minEstimatedFiles        int
	fallbackRemainingMinutes float64
	uncertaintyMultiplier    float64
}

func runtimePolicyFromConfig(cfg *config.WorkflowConfig) runtimePolicy {
	return runtimePolicy{
		eventBufferSize:     cfg.OrchestratorEventBufferSize(),
		runSignalBufferSize: cfg.OrchestratorRunSignalBufferSize(),
		issueCacheSize:      cfg.OrchestratorIssueCacheSize(),
		runShutdownTimeout:  durationMs(cfg.OrchestratorRunShutdownTimeoutMs()),
		stopGraceTimeout:    durationMs(cfg.OrchestratorStopGraceTimeoutMs()),
		gitCommandTimeout:   durationMs(cfg.OrchestratorGitCommandTimeoutMs()),
		shutdown: ShutdownConfig{
			DrainTimeout:   durationMs(cfg.OrchestratorShutdownDrainTimeoutMs()),
			CleanupTimeout: durationMs(cfg.OrchestratorShutdownCleanupTimeoutMs()),
			PollInterval:   durationMs(cfg.OrchestratorShutdownPollIntervalMs()),
		},
		backoff: backoffPolicy{
			strategy:       cfg.PollingBackoffStrategy(),
			continuationMs: cfg.OrchestratorContinuationBackoffMs(),
			failureBaseMs:  cfg.OrchestratorFailureBackoffBaseMs(),
			multiplier:     cfg.OrchestratorFailureBackoffMultiplier(),
			jitterPercent:  cfg.OrchestratorBackoffJitterPercent(),
		},
		snapshot: snapshotPolicy{
			diffTimeout: durationMs(cfg.OrchestratorSnapshotDiffTimeoutMs()),
			stage: stagePolicy{
				testingAfter:                durationMs(cfg.OrchestratorStageTestingAfterMs()),
				reviewingAfter:              durationMs(cfg.OrchestratorStageReviewingAfterMs()),
				reviewingMaxTokensPerMinute: cfg.OrchestratorStageReviewingMaxTokensPerMinute(),
			},
			eta: etaPolicy{
				minElapsed:               durationMs(cfg.OrchestratorETAMinElapsedMs()),
				minFilesPerMinute:        cfg.OrchestratorETAMinFilesPerMinute(),
				minTokensPerMinute:       cfg.OrchestratorETAMinTokensPerMinute(),
				mediumConfidenceAfter:    durationMs(cfg.OrchestratorETAMediumConfidenceAfterMs()),
				highConfidenceAfter:      durationMs(cfg.OrchestratorETAHighConfidenceAfterMs()),
				highConfidenceMinStage:   cfg.OrchestratorETAHighConfidenceMinStage(),
				estimatedFilesMultiplier: cfg.OrchestratorETAEstimatedFilesMultiplier(),
				minEstimatedFiles:        cfg.OrchestratorETAMinEstimatedFiles(),
				fallbackRemainingMinutes: cfg.OrchestratorETAFallbackRemainingMinutes(),
				uncertaintyMultiplier:    cfg.OrchestratorETAUncertaintyMultiplier(),
			},
		},
	}
}

func durationMs(value int) time.Duration {
	return time.Duration(value) * time.Millisecond
}

func (o *Orchestrator) runtimePolicy() runtimePolicy {
	return runtimePolicyFromConfig(o.currentConfig())
}

// ShutdownConfig returns the current workflow-derived shutdown policy.
func (o *Orchestrator) ShutdownConfig() ShutdownConfig {
	return o.runtimePolicy().shutdown
}
