package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/junhoyeo/contrabass/internal/ipc"
	"github.com/junhoyeo/contrabass/internal/tmux"
	"github.com/junhoyeo/contrabass/internal/types"
)

var (
	errTmuxRunnerAlreadyStopped = errors.New("tmux runner process already stopped")
	errTmuxRunnerStopFailed     = errors.New("tmux runner stop failed")
)

type TmuxRunnerConfig struct {
	TeamName         string
	AgentType        string
	BinaryPath       string
	Session          *tmux.Session
	Registry         *tmux.CLIRegistry
	HeartbeatMonitor ipc.HeartbeatWriter
	EventLogger      ipc.EventWriter
	DispatchQueue    ipc.Dispatcher
	PollInterval     time.Duration
	Logger           *slog.Logger
}

type TmuxRunner struct {
	teamName         string
	agentType        string
	binaryPath       string
	session          *tmux.Session
	registry         *tmux.CLIRegistry
	heartbeatMonitor ipc.HeartbeatWriter
	eventLogger      ipc.EventWriter
	dispatchQueue    ipc.Dispatcher
	pollInterval     time.Duration
	logger           *slog.Logger

	pidSeq atomic.Int64

	mu    sync.Mutex
	procs map[int]*tmuxProcess
}

type tmuxProcess struct {
	pid       int
	paneID    string
	workerID  string
	taskID    string
	workspace string

	promptPath     string
	exitMarkerPath string
	events         chan types.AgentEvent
	done           chan error
	finished       chan struct{}

	cancel     context.CancelFunc
	finishOnce sync.Once
	removeOnce sync.Once
}

func NewTmuxRunner(cfg TmuxRunnerConfig) *TmuxRunner {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &TmuxRunner{
		teamName:         cfg.TeamName,
		agentType:        cfg.AgentType,
		binaryPath:       cfg.BinaryPath,
		session:          cfg.Session,
		registry:         cfg.Registry,
		heartbeatMonitor: cfg.HeartbeatMonitor,
		eventLogger:      cfg.EventLogger,
		dispatchQueue:    cfg.DispatchQueue,
		pollInterval:     cfg.PollInterval,
		logger:           cfg.Logger,
		procs:            make(map[int]*tmuxProcess),
	}
}

func (p *tmuxProcess) finish(err error) {
	p.finishOnce.Do(func() {
		p.done <- err
		close(p.done)
		close(p.finished)
	})
}

func (p *tmuxProcess) remove(r *TmuxRunner) {
	p.removeOnce.Do(func() {
		r.mu.Lock()
		delete(r.procs, p.pid)
		r.mu.Unlock()
	})
}

func (r *TmuxRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	if r.session == nil {
		return nil, errors.New("tmux session is nil")
	}
	if r.registry == nil {
		return nil, errors.New("tmux cli registry is nil")
	}

	if err := r.session.CreateIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("ensure tmux session %q: %w", r.session.Name, err)
	}

	cliCfg, err := r.registry.Get(r.agentType)
	if err != nil {
		return nil, fmt.Errorf("resolve tmux cli config: %w", err)
	}

	binaryPath := strings.TrimSpace(r.binaryPath)
	if binaryPath == "" {
		binaryPath = strings.TrimSpace(cliCfg.BinaryPath)
	}
	if binaryPath == "" {
		return nil, fmt.Errorf("binary path is empty for agent type %q", r.agentType)
	}

	taskSeed := buildTeamTaskSeed(issue, prompt)
	promptPath, _, err := writeTeamPromptFile(workspace, "tmux", issue, taskSeed, prompt)
	if err != nil {
		return nil, fmt.Errorf("write tmux prompt file: %w", err)
	}

	pid := int(r.pidSeq.Add(1))
	// The "tmux-" prefix keeps these synthetic IDs out of the coordinator's
	// "worker-<n>" heartbeat namespace, where a collision would keep a dead
	// coordinator worker's heartbeat fresh and mask its staleness.
	workerID := fmt.Sprintf("tmux-worker-%d", pid)
	taskID := firstNonEmpty(issue.ID, issue.Identifier, workerID)
	cliArgs := cliCfg.BuildArgs(workspace, promptPath)

	exitMarkerPath, err := prepareExitMarker(workspace, pid)
	if err != nil {
		return nil, fmt.Errorf("prepare exit marker: %w", err)
	}

	bootstrap := tmux.NewWorkerBootstrap(r.session, tmux.BootstrapConfig{
		WorkerID:       workerID,
		TeamName:       r.teamName,
		WorkDir:        workspace,
		CLICommand:     binaryPath,
		CLIArgs:        cliArgs,
		Env:            cliCfg.Env,
		PromptMode:     cliCfg.PromptMode,
		PromptPath:     promptPath,
		ExitMarkerPath: exitMarkerPath,
	})

	paneID, err := bootstrap.Bootstrap(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap tmux worker %q: %w", workerID, err)
	}

	state := &tmuxProcess{
		pid:            pid,
		paneID:         paneID,
		workerID:       workerID,
		taskID:         taskID,
		workspace:      workspace,
		promptPath:     promptPath,
		exitMarkerPath: exitMarkerPath,
		events:         make(chan types.AgentEvent, 128),
		done:           make(chan error, 1),
		finished:       make(chan struct{}),
	}

	if r.dispatchQueue != nil {
		if dispatchErr := r.dispatchQueue.Dispatch(r.teamName, ipc.DispatchEntry{
			TaskID:       taskID,
			WorkerID:     workerID,
			Prompt:       strings.TrimSpace(prompt),
			DispatchedAt: time.Now(),
		}); dispatchErr != nil {
			r.logger.Warn("tmux dispatch write failed", "team", r.teamName, "task_id", taskID, "error", dispatchErr)
		}
	}

	monitorCtx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel

	r.mu.Lock()
	r.procs[pid] = state
	r.mu.Unlock()

	go r.monitorProcess(monitorCtx, bootstrap, state)
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Stop(&AgentProcess{PID: pid, SessionID: paneID})
		case <-state.finished:
		}
	}()

	return &AgentProcess{
		PID:       pid,
		SessionID: paneID,
		Events:    state.events,
		Done:      state.done,
		serverURL: promptPath,
	}, nil
}

func (r *TmuxRunner) Stop(proc *AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	r.mu.Lock()
	state, ok := r.procs[proc.PID]
	if ok {
		delete(r.procs, proc.PID)
	}
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: pid %d", errTmuxRunnerAlreadyStopped, proc.PID)
	}

	state.cancel()
	state.remove(r)

	killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer killCancel()
	killErr := r.session.KillPane(killCtx, state.paneID)
	state.finish(killErr)
	if killErr != nil {
		return fmt.Errorf("%w: %v", errTmuxRunnerStopFailed, killErr)
	}

	return nil
}

func (r *TmuxRunner) Close() error {
	r.mu.Lock()
	states := make([]*tmuxProcess, 0, len(r.procs))
	for _, proc := range r.procs {
		states = append(states, proc)
	}
	r.procs = make(map[int]*tmuxProcess)
	r.mu.Unlock()

	var errs []error
	for _, proc := range states {
		proc.cancel()
		proc.remove(r)
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		killErr := r.session.KillPane(killCtx, proc.paneID)
		killCancel()
		if killErr != nil {
			errs = append(errs, killErr)
		}
		proc.finish(killErr)
	}

	return errors.Join(errs...)
}

func (r *TmuxRunner) monitorProcess(ctx context.Context, bootstrap *tmux.WorkerBootstrap, proc *tmuxProcess) {
	defer close(proc.events)
	defer proc.remove(r)

	emit := func(eventType string, data map[string]interface{}) {
		event := types.AgentEvent{Type: eventType, Data: data, Timestamp: time.Now()}
		select {
		case <-ctx.Done():
		case proc.events <- event:
		}
	}

	// Completion comes from the exit marker the pane's shell writes when the
	// CLI command exits — never from pane liveness. The interactive shell
	// keeps the pane alive after the command exits, and a pane that dies
	// without a marker (tmux destroys dead panes, so IsWorkerAlive errors)
	// means the command was killed, not that it finished.
	checkAlive := func(logStarted bool) (bool, error) {
		exitCode, exited, markerErr := readExitMarker(proc.exitMarkerPath)

		paneAlive := true
		var paneErr error
		if !exited && markerErr == nil {
			paneAlive, paneErr = bootstrap.IsWorkerAlive(ctx, proc.paneID)
			if paneErr != nil || !paneAlive {
				// The command may have exited and written the marker just
				// before the pane closed; re-check before calling it a crash.
				exitCode, exited, markerErr = readExitMarker(proc.exitMarkerPath)
			}
		}

		var failure error
		switch {
		case markerErr != nil:
			failure = fmt.Errorf("read exit marker for pane %s: %w", proc.paneID, markerErr)
		case exited && exitCode != 0:
			failure = fmt.Errorf("worker command exited with code %d", exitCode)
		case !exited && paneErr != nil:
			failure = fmt.Errorf("worker pane %s died before writing exit marker: %w", proc.paneID, paneErr)
		case !exited && !paneAlive:
			failure = fmt.Errorf("worker pane %s died before writing exit marker", proc.paneID)
		}

		now := time.Now()

		status := "running"
		if failure != nil {
			status = "error"
		} else if exited {
			status = "stopped"
		}

		if r.heartbeatMonitor != nil {
			if hbErr := r.heartbeatMonitor.Write(r.teamName, ipc.Heartbeat{
				WorkerID:    proc.workerID,
				PID:         proc.pid,
				CurrentTask: proc.taskID,
				Status:      status,
				Timestamp:   now,
			}); hbErr != nil {
				r.logger.Warn("tmux heartbeat write failed", "team", r.teamName, "worker_id", proc.workerID, "error", hbErr)
			}
		}

		if logStarted {
			if r.eventLogger != nil {
				if logErr := r.eventLogger.Log(r.teamName, ipc.Event{
					Type:      "worker_started",
					WorkerID:  proc.workerID,
					TaskID:    proc.taskID,
					Data:      map[string]interface{}{"pane_id": proc.paneID},
					Timestamp: now,
				}); logErr != nil {
					r.logger.Warn("tmux worker_started log failed", "team", r.teamName, "worker_id", proc.workerID, "error", logErr)
				}
			}

			if r.dispatchQueue != nil {
				if ackErr := r.dispatchQueue.Ack(r.teamName, proc.taskID, proc.workerID); ackErr != nil {
					r.logger.Warn("tmux dispatch ack failed", "team", r.teamName, "task_id", proc.taskID, "worker_id", proc.workerID, "error", ackErr)
				}
			}
		}

		if failure != nil {
			return false, failure
		}
		if exited {
			if r.eventLogger != nil {
				if logErr := r.eventLogger.Log(r.teamName, ipc.Event{
					Type:      "worker_stopped",
					WorkerID:  proc.workerID,
					TaskID:    proc.taskID,
					Data:      map[string]interface{}{"pane_id": proc.paneID, "exit_code": exitCode},
					Timestamp: now,
				}); logErr != nil {
					r.logger.Warn("tmux worker_stopped log failed", "team", r.teamName, "worker_id", proc.workerID, "error", logErr)
				}
			}
			if r.dispatchQueue != nil {
				if completeErr := r.dispatchQueue.Complete(r.teamName, proc.taskID); completeErr != nil {
					r.logger.Warn("tmux dispatch complete failed", "team", r.teamName, "task_id", proc.taskID, "error", completeErr)
				}
			}
			return true, nil
		}

		return false, nil
	}

	emit("turn/started", map[string]interface{}{
		"pane_id":     proc.paneID,
		"worker_id":   proc.workerID,
		"task_id":     proc.taskID,
		"prompt_file": proc.promptPath,
	})

	stopped, err := checkAlive(true)
	if err != nil {
		emit("turn/failed", map[string]interface{}{
			"pane_id":   proc.paneID,
			"worker_id": proc.workerID,
			"task_id":   proc.taskID,
			"error":     err.Error(),
		})
		proc.finish(err)
		return
	}
	if stopped {
		emit("task/completed", map[string]interface{}{
			"pane_id":   proc.paneID,
			"worker_id": proc.workerID,
			"task_id":   proc.taskID,
		})
		r.cleanupPane(proc)
		proc.finish(nil)
		return
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stopped, err := checkAlive(false)
			if err != nil {
				emit("turn/failed", map[string]interface{}{
					"pane_id":   proc.paneID,
					"worker_id": proc.workerID,
					"task_id":   proc.taskID,
					"error":     err.Error(),
				})
				proc.finish(err)
				return
			}
			if stopped {
				emit("task/completed", map[string]interface{}{
					"pane_id":   proc.paneID,
					"worker_id": proc.workerID,
					"task_id":   proc.taskID,
				})
				r.cleanupPane(proc)
				proc.finish(nil)
				return
			}
		}
	}
}

// cleanupPane kills the pane after a successful completion: the CLI ran
// inside the pane's interactive shell, so the pane stays alive after the
// command exits. Failed panes are intentionally kept for post-mortem.
func (r *TmuxRunner) cleanupPane(proc *tmuxProcess) {
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.session.KillPane(killCtx, proc.paneID); err != nil {
		r.logger.Warn("tmux pane cleanup failed", "team", r.teamName, "pane_id", proc.paneID, "error", err)
	}
}

// prepareExitMarker returns the file the pane shell writes the CLI's exit
// code to. A stale marker left by a previous run of the same workspace is
// removed first so an old exit code cannot complete the new task instantly.
func prepareExitMarker(workspace string, pid int) (string, error) {
	dir := filepath.Join(workspace, ".contrabass")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("task-exit-%d", pid))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

// readExitMarker reports the exit code recorded by the pane shell. found is
// false while the command is still running, or while the marker write is
// mid-flight and the file is momentarily empty.
func readExitMarker(path string) (code int, found bool, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, false, nil
		}
		return 0, false, readErr
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, false, nil
	}

	code, parseErr := strconv.Atoi(content)
	if parseErr != nil {
		return 0, false, fmt.Errorf("parse exit marker %s: %w", path, parseErr)
	}
	return code, true, nil
}
