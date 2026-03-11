package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/team"
	"github.com/junhoyeo/contrabass/internal/types"
)

const workerHeartbeatInterval = 5 * time.Second

var teamWorkerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run as a team worker in a tmux pane",
	RunE:  runTeamWorker,
}

func init() {
	teamWorkerCmd.Flags().StringP("config", "c", "", "path to WORKFLOW.md file (required)")
	teamWorkerCmd.Flags().StringP("name", "n", "", "team name (required)")
	teamWorkerCmd.Flags().String("worker-id", "", "worker ID (required)")
	teamWorkerCmd.Flags().String("task-file", "", "path to task prompt file")

	_ = teamWorkerCmd.MarkFlagRequired("config")
	_ = teamWorkerCmd.MarkFlagRequired("name")
	_ = teamWorkerCmd.MarkFlagRequired("worker-id")

	teamCmd.AddCommand(teamWorkerCmd)
}

func runTeamWorker(cmd *cobra.Command, args []string) error {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("getting config flag: %w", err)
	}

	teamName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("getting name flag: %w", err)
	}

	workerID, err := cmd.Flags().GetString("worker-id")
	if err != nil {
		return fmt.Errorf("getting worker-id flag: %w", err)
	}

	taskFile, err := cmd.Flags().GetString("task-file")
	if err != nil {
		return fmt.Errorf("getting task-file flag: %w", err)
	}

	cfg, err := config.ParseWorkflow(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	paths := team.NewPaths(cfg.TeamStateDir())
	store := team.NewStore(paths)
	heartbeats := team.NewHeartbeatMonitor(store, paths, 3*workerHeartbeatInterval)
	events := team.NewEventLogger(paths)
	defer events.Close()

	if err := store.EnsureDirs(teamName); err != nil {
		return fmt.Errorf("ensuring team state dirs: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	parentCtx := context.Background()
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChan)

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-signalChan:
			logger.Info("received signal, stopping worker", "team", teamName, "worker_id", workerID)
			cancel()
		}
	}()

	if err := events.Log(teamName, team.LoggedEvent{
		Type:     "worker_started",
		WorkerID: workerID,
		Data: map[string]interface{}{
			"agent_type": cfg.AgentType(),
		},
	}); err != nil {
		logger.Warn("failed to log worker_started event", "team", teamName, "worker_id", workerID, "error", err)
	}

	logger.Info("worker started", "team", teamName, "worker_id", workerID, "task_file", taskFile)

	var stateMu sync.RWMutex
	currentTask := ""
	status := "idle"
	setState := func(nextStatus, nextTask string) {
		stateMu.Lock()
		status = nextStatus
		currentTask = nextTask
		stateMu.Unlock()
	}
	readState := func() (string, string) {
		stateMu.RLock()
		defer stateMu.RUnlock()
		return status, currentTask
	}

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(workerHeartbeatInterval)
		defer ticker.Stop()

		writeHeartbeat := func() {
			hbStatus, hbTask := readState()
			hb := team.Heartbeat{
				WorkerID:    workerID,
				PID:         os.Getpid(),
				CurrentTask: hbTask,
				Status:      hbStatus,
				Timestamp:   time.Now(),
			}
			if err := heartbeats.Write(teamName, hb); err != nil {
				logger.Warn("failed to write heartbeat", "team", teamName, "worker_id", workerID, "error", err)
			}
		}

		writeHeartbeat()
		for {
			select {
			case <-ctx.Done():
				writeHeartbeat()
				return
			case <-ticker.C:
				writeHeartbeat()
			}
		}
	}()

	// Worker processes run inside tmux panes and must use goroutine mode
	// to avoid recursive tmux spawning. Override the config to force goroutine mode.
	workerCfg := cfg.Clone()
	workerCfg.Team.WorkerMode = "goroutine"

	runner, err := createRunner(workerCfg, teamName, logger)
	if err != nil {
		cancel()
		<-heartbeatDone
		return fmt.Errorf("creating agent runner: %w", err)
	}
	defer runner.Close()

	if taskFile == "" {
		logger.Info("no task file provided; worker is idle and waiting", "team", teamName, "worker_id", workerID)
		<-ctx.Done()
		setState("stopped", "")
		<-heartbeatDone

		if err := events.Log(teamName, team.LoggedEvent{Type: "worker_stopped", WorkerID: workerID}); err != nil {
			logger.Warn("failed to log worker_stopped event", "team", teamName, "worker_id", workerID, "error", err)
		}
		logger.Info("worker stopped", "team", teamName, "worker_id", workerID)
		return nil
	}

	promptBytes, err := os.ReadFile(taskFile)
	if err != nil {
		setState("failed", "")
		cancel()
		<-heartbeatDone
		_ = events.Log(teamName, team.LoggedEvent{
			Type:     "task_failed",
			WorkerID: workerID,
			Data: map[string]interface{}{
				"task_file": taskFile,
				"error":     err.Error(),
			},
		})
		return fmt.Errorf("reading task file: %w", err)
	}

	prompt := string(promptBytes)
	taskID := filepath.Base(taskFile)
	setState("working", taskID)

	if err := events.Log(teamName, team.LoggedEvent{
		Type:     "task_claimed",
		WorkerID: workerID,
		TaskID:   taskID,
		Data: map[string]interface{}{
			"task_file": taskFile,
		},
	}); err != nil {
		logger.Warn("failed to log task_claimed event", "team", teamName, "worker_id", workerID, "task_id", taskID, "error", err)
	}

	logger.Info("task claimed", "team", teamName, "worker_id", workerID, "task_id", taskID, "task_file", taskFile)

	workDir, err := os.Getwd()
	if err != nil {
		setState("failed", taskID)
		cancel()
		<-heartbeatDone
		return fmt.Errorf("getting working directory: %w", err)
	}

	issue := types.Issue{
		ID:         taskID,
		Identifier: taskID,
		Title:      fmt.Sprintf("Team worker task %s", taskID),
	}

	proc, err := runner.Start(ctx, issue, workDir, prompt)
	if err != nil {
		setState("failed", taskID)
		cancel()
		<-heartbeatDone
		_ = events.Log(teamName, team.LoggedEvent{
			Type:     "task_failed",
			WorkerID: workerID,
			TaskID:   taskID,
			Data: map[string]interface{}{
				"error": err.Error(),
			},
		})
		return fmt.Errorf("starting agent process: %w", err)
	}

	go func() {
		for range proc.Events {
		}
	}()

	var runErr error
	select {
	case runErr = <-proc.Done:
	case <-ctx.Done():
		runErr = ctx.Err()
		_ = runner.Stop(proc)
	}

	if runErr != nil {
		setState("failed", taskID)
		_ = events.Log(teamName, team.LoggedEvent{
			Type:     "task_failed",
			WorkerID: workerID,
			TaskID:   taskID,
			Data: map[string]interface{}{
				"error": runErr.Error(),
			},
		})
		_ = events.Log(teamName, team.LoggedEvent{Type: "worker_stopped", WorkerID: workerID})
		cancel()
		<-heartbeatDone
		logger.Error("task failed", "team", teamName, "worker_id", workerID, "task_id", taskID, "error", runErr)
		return fmt.Errorf("running agent process: %w", runErr)
	}

	setState("completed", taskID)
	if err := events.Log(teamName, team.LoggedEvent{
		Type:     "task_completed",
		WorkerID: workerID,
		TaskID:   taskID,
		Data: map[string]interface{}{
			"task_file": taskFile,
		},
	}); err != nil {
		logger.Warn("failed to log task_completed event", "team", teamName, "worker_id", workerID, "task_id", taskID, "error", err)
	}
	logger.Info("task completed", "team", teamName, "worker_id", workerID, "task_id", taskID)

	setState("stopped", "")
	if err := events.Log(teamName, team.LoggedEvent{Type: "worker_stopped", WorkerID: workerID}); err != nil {
		logger.Warn("failed to log worker_stopped event", "team", teamName, "worker_id", workerID, "error", err)
	}
	logger.Info("worker stopped", "team", teamName, "worker_id", workerID)

	cancel()
	<-heartbeatDone
	return nil
}
