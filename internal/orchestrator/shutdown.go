package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/log"
)

type ShutdownConfig struct {
	DrainTimeout   time.Duration
	CleanupTimeout time.Duration
	PollInterval   time.Duration
}

func DefaultShutdownConfig() ShutdownConfig {
	return runtimePolicyFromConfig(nil).shutdown
}

func (o *Orchestrator) RunningCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return len(o.running)
}

func GracefulShutdown(
	cancel context.CancelFunc,
	orch *Orchestrator,
	cfg ShutdownConfig,
	logger *log.Logger,
) error {
	if orch == nil {
		return errors.New("orchestrator is nil")
	}

	cfg = normalizeShutdownConfig(cfg)

	if cancel != nil {
		cancel()
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.DrainTimeout)
	defer drainCancel()

	if !waitForDrain(drainCtx, orch, cfg.PollInterval) && logger != nil {
		logger.Warn("drain timeout reached; forcing shutdown")
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cfg.CleanupTimeout)
	defer cleanupCancel()

	return orch.gracefulShutdown(cleanupCtx)
}

func waitForDrain(ctx context.Context, orch *Orchestrator, pollInterval time.Duration) bool {
	if orch.RunningCount() == 0 {
		return true
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return orch.RunningCount() == 0
		case <-ticker.C:
			if orch.RunningCount() == 0 {
				return true
			}
		}
	}
}

func normalizeShutdownConfig(cfg ShutdownConfig) ShutdownConfig {
	defaults := DefaultShutdownConfig()

	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = defaults.DrainTimeout
	}
	if cfg.CleanupTimeout <= 0 {
		cfg.CleanupTimeout = defaults.CleanupTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}

	return cfg
}
