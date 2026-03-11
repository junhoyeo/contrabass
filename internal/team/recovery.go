package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

type DiagnosisReport struct {
	PhaseState      *types.TeamPhaseState
	StaleWorkers    []Heartbeat
	OrphanedTasks   []types.TeamTask
	PendingDispatch []DispatchEntry
}

type Recovery struct {
	store      *Store
	paths      *Paths
	heartbeats *HeartbeatMonitor
	tasks      *TaskRegistry
	dispatch   *DispatchQueue
	logger     *slog.Logger
}

func NewRecovery(
	store *Store,
	paths *Paths,
	heartbeats *HeartbeatMonitor,
	tasks *TaskRegistry,
	dispatch *DispatchQueue,
	logger *slog.Logger,
) *Recovery {
	if logger == nil {
		logger = slog.Default()
	}

	return &Recovery{
		store:      store,
		paths:      paths,
		heartbeats: heartbeats,
		tasks:      tasks,
		dispatch:   dispatch,
		logger:     logger,
	}
}

func (r *Recovery) Diagnose(teamName string) (*DiagnosisReport, error) {
	report := &DiagnosisReport{}

	phaseState, err := r.store.LoadPhaseState(teamName)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !isJSONUnmarshalError(err) {
			return nil, fmt.Errorf("diagnose: load phase state: %w", err)
		}
		if isJSONUnmarshalError(err) {
			r.logger.Warn("skipping malformed phase state", "team", teamName, "error", err)
		}
	} else {
		report.PhaseState = phaseState
	}

	allHeartbeats, err := r.heartbeats.ListAll(teamName)
	if err != nil {
		return nil, fmt.Errorf("diagnose: list heartbeats: %w", err)
	}

	staleWorkers := make([]Heartbeat, 0, len(allHeartbeats))
	freshWorkerIDs := make(map[string]struct{}, len(allHeartbeats))
	for _, hb := range allHeartbeats {
		stale, staleErr := r.heartbeats.IsStale(teamName, hb.WorkerID)
		if staleErr != nil {
			return nil, fmt.Errorf("diagnose: check stale heartbeat for %s: %w", hb.WorkerID, staleErr)
		}
		if stale {
			staleWorkers = append(staleWorkers, hb)
			continue
		}
		freshWorkerIDs[hb.WorkerID] = struct{}{}
	}
	report.StaleWorkers = staleWorkers

	tasks, err := r.tasks.ListTasks(teamName)
	if err != nil {
		return nil, fmt.Errorf("diagnose: list tasks: %w", err)
	}

	orphanedTasks := make([]types.TeamTask, 0)
	for _, task := range tasks {
		if task.Status != types.TaskInProgress || task.Claim == nil {
			continue
		}
		if _, alive := freshWorkerIDs[task.Claim.WorkerID]; !alive {
			orphanedTasks = append(orphanedTasks, task)
		}
	}
	report.OrphanedTasks = orphanedTasks

	pendingDispatch, err := r.dispatch.GetPending(teamName)
	if err != nil {
		return nil, fmt.Errorf("diagnose: list pending dispatch: %w", err)
	}
	report.PendingDispatch = pendingDispatch

	return report, nil
}

func (r *Recovery) Recover(ctx context.Context, teamName string) error {
	report, err := r.Diagnose(teamName)
	if err != nil {
		return err
	}

	now := time.Now()
	releasedTasks := 0
	for _, task := range report.OrphanedTasks {
		if err := ctx.Err(); err != nil {
			return err
		}

		if task.Status != types.TaskInProgress || task.Claim == nil {
			continue
		}

		task.Status = types.TaskPending
		task.Claim = nil
		task.Version++
		task.UpdatedAt = now

		if err := r.store.WriteJSON(r.paths.TaskPath(teamName, task.ID), &task); err != nil {
			return fmt.Errorf("recover: release orphaned task %s: %w", task.ID, err)
		}
		releasedTasks++
		r.logger.Info("released orphaned task claim", "team", teamName, "task", task.ID)
	}

	cleanedHeartbeats := 0
	for _, hb := range report.StaleWorkers {
		if err := ctx.Err(); err != nil {
			return err
		}

		path := r.paths.HeartbeatPath(teamName, hb.WorkerID)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("recover: remove stale heartbeat for %s: %w", hb.WorkerID, err)
		}

		_ = os.Remove(filepath.Dir(path))

		cleanedHeartbeats++
		r.logger.Info("cleaned stale heartbeat", "team", teamName, "worker", hb.WorkerID)
	}

	r.logger.Info(
		"recovery completed",
		"team", teamName,
		"released_orphaned_tasks", releasedTasks,
		"cleaned_stale_heartbeats", cleanedHeartbeats,
	)

	return nil
}
