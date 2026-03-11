package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	promptPath string
	events     chan types.AgentEvent
	done       chan error
	finished   chan struct{}

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
	workerID := fmt.Sprintf("worker-%d", pid)
	taskID := firstNonEmpty(issue.ID, issue.Identifier, workerID)
	cliArgs := cliCfg.BuildArgs(workspace, promptPath)

	bootstrap := tmux.NewWorkerBootstrap(r.session, tmux.BootstrapConfig{
		WorkerID:   workerID,
		TeamName:   r.teamName,
		WorkDir:    workspace,
		CLICommand: binaryPath,
		CLIArgs:    cliArgs,
		Env:        cliCfg.Env,
	})

	paneID, err := bootstrap.Bootstrap(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap tmux worker %q: %w", workerID, err)
	}

	state := &tmuxProcess{
		pid:        pid,
		paneID:     paneID,
		workerID:   workerID,
		taskID:     taskID,
		workspace:  workspace,
		promptPath: promptPath,
		events:     make(chan types.AgentEvent, 128),
		done:       make(chan error, 1),
		finished:   make(chan struct{}),
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

	checkAlive := func(logStarted bool) (bool, error) {
		alive, err := bootstrap.IsWorkerAlive(ctx, proc.paneID)
		now := time.Now()

		status := "running"
		if err != nil {
			status = "error"
		} else if !alive {
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

		if err != nil {
			return false, err
		}
		if !alive {
			if r.eventLogger != nil {
				if logErr := r.eventLogger.Log(r.teamName, ipc.Event{
					Type:      "worker_stopped",
					WorkerID:  proc.workerID,
					TaskID:    proc.taskID,
					Data:      map[string]interface{}{"pane_id": proc.paneID},
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
				proc.finish(nil)
				return
			}
		}
	}
}
