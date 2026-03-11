package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	HeartbeatMonitor any
	EventLogger      any
	DispatchQueue    any
	PollInterval     time.Duration
	Logger           *slog.Logger
}

type TmuxRunner struct {
	teamName         string
	agentType        string
	binaryPath       string
	session          *tmux.Session
	registry         *tmux.CLIRegistry
	heartbeatMonitor any
	eventLogger      any
	dispatchQueue    any
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
	cliCommand := buildCLICommand(binaryPath, cliCfg.BuildArgs(workspace, promptPath))

	bootstrap := tmux.NewWorkerBootstrap(r.session, tmux.BootstrapConfig{
		WorkerID:   workerID,
		TeamName:   r.teamName,
		WorkDir:    workspace,
		CLICommand: cliCommand,
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
		if dispatchErr := callDispatch(r.dispatchQueue, r.teamName, taskID, workerID, strings.TrimSpace(prompt)); dispatchErr != nil {
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

	killErr := r.session.KillPane(context.Background(), state.paneID)
	state.finish(nil)
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
		if err := r.session.KillPane(context.Background(), proc.paneID); err != nil {
			errs = append(errs, err)
		}
		proc.finish(nil)
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
			if hbErr := callHeartbeatWrite(r.heartbeatMonitor, r.teamName, proc.workerID, proc.pid, proc.taskID, status, now); hbErr != nil {
				r.logger.Warn("tmux heartbeat write failed", "team", r.teamName, "worker_id", proc.workerID, "error", hbErr)
			}
		}

		if logStarted {
			if r.eventLogger != nil {
				if logErr := callEventLog(r.eventLogger, r.teamName, "worker_started", proc.workerID, proc.taskID, map[string]interface{}{"pane_id": proc.paneID}, now); logErr != nil {
					r.logger.Warn("tmux worker_started log failed", "team", r.teamName, "worker_id", proc.workerID, "error", logErr)
				}
			}

			if r.dispatchQueue != nil {
				if ackErr := callDispatchAck(r.dispatchQueue, r.teamName, proc.taskID, proc.workerID); ackErr != nil {
					r.logger.Warn("tmux dispatch ack failed", "team", r.teamName, "task_id", proc.taskID, "worker_id", proc.workerID, "error", ackErr)
				}
			}
		}

		if err != nil {
			return false, err
		}
		if !alive {
			if r.eventLogger != nil {
				if logErr := callEventLog(r.eventLogger, r.teamName, "worker_stopped", proc.workerID, proc.taskID, map[string]interface{}{"pane_id": proc.paneID}, now); logErr != nil {
					r.logger.Warn("tmux worker_stopped log failed", "team", r.teamName, "worker_id", proc.workerID, "error", logErr)
				}
			}
			if r.dispatchQueue != nil {
				if completeErr := callDispatchComplete(r.dispatchQueue, r.teamName, proc.taskID); completeErr != nil {
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
			proc.finish(nil)
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

func buildCLICommand(binaryPath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strings.TrimSpace(binaryPath))
	parts = append(parts, args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func callHeartbeatWrite(monitor any, teamName, workerID string, pid int, taskID, status string, timestamp time.Time) error {
	if monitor == nil {
		return nil
	}

	method := reflect.ValueOf(monitor).MethodByName("Write")
	if !method.IsValid() {
		return errors.New("heartbeat monitor does not implement Write")
	}
	if method.Type().NumIn() != 2 {
		return errors.New("heartbeat monitor Write signature is invalid")
	}

	hb := reflect.New(method.Type().In(1)).Elem()
	setStructField(hb, "WorkerID", workerID)
	setStructField(hb, "PID", pid)
	setStructField(hb, "CurrentTask", taskID)
	setStructField(hb, "Status", status)
	setStructField(hb, "Timestamp", timestamp)

	results := method.Call([]reflect.Value{reflect.ValueOf(teamName), hb})
	if len(results) == 1 && !results[0].IsNil() {
		return results[0].Interface().(error)
	}

	return nil
}

func callEventLog(logger any, teamName, eventType, workerID, taskID string, data map[string]interface{}, timestamp time.Time) error {
	if logger == nil {
		return nil
	}

	method := reflect.ValueOf(logger).MethodByName("Log")
	if !method.IsValid() {
		return errors.New("event logger does not implement Log")
	}
	if method.Type().NumIn() != 2 {
		return errors.New("event logger Log signature is invalid")
	}

	event := reflect.New(method.Type().In(1)).Elem()
	setStructField(event, "Type", eventType)
	setStructField(event, "WorkerID", workerID)
	setStructField(event, "TaskID", taskID)
	setStructField(event, "Data", data)
	setStructField(event, "Timestamp", timestamp)

	results := method.Call([]reflect.Value{reflect.ValueOf(teamName), event})
	if len(results) == 1 && !results[0].IsNil() {
		return results[0].Interface().(error)
	}

	return nil
}

func callDispatch(dispatchQueue any, teamName, taskID, workerID, prompt string) error {
	if dispatchQueue == nil {
		return nil
	}

	method := reflect.ValueOf(dispatchQueue).MethodByName("Dispatch")
	if !method.IsValid() {
		return errors.New("dispatch queue does not implement Dispatch")
	}
	if method.Type().NumIn() != 2 {
		return errors.New("dispatch queue Dispatch signature is invalid")
	}

	entry := reflect.New(method.Type().In(1)).Elem()
	setStructField(entry, "TaskID", taskID)
	setStructField(entry, "WorkerID", workerID)
	setStructField(entry, "Prompt", prompt)
	setStructField(entry, "DispatchedAt", time.Now())

	results := method.Call([]reflect.Value{reflect.ValueOf(teamName), entry})
	if len(results) == 1 && !results[0].IsNil() {
		return results[0].Interface().(error)
	}

	return nil
}

func callDispatchAck(dispatchQueue any, teamName, taskID, workerID string) error {
	if dispatchQueue == nil {
		return nil
	}

	method := reflect.ValueOf(dispatchQueue).MethodByName("Ack")
	if !method.IsValid() {
		return errors.New("dispatch queue does not implement Ack")
	}
	results := method.Call([]reflect.Value{
		reflect.ValueOf(teamName),
		reflect.ValueOf(taskID),
		reflect.ValueOf(workerID),
	})
	if len(results) == 1 && !results[0].IsNil() {
		return results[0].Interface().(error)
	}

	return nil
}

func callDispatchComplete(dispatchQueue any, teamName, taskID string) error {
	if dispatchQueue == nil {
		return nil
	}

	method := reflect.ValueOf(dispatchQueue).MethodByName("Complete")
	if !method.IsValid() {
		return errors.New("dispatch queue does not implement Complete")
	}
	results := method.Call([]reflect.Value{reflect.ValueOf(teamName), reflect.ValueOf(taskID)})
	if len(results) == 1 && !results[0].IsNil() {
		return results[0].Interface().(error)
	}

	return nil
}

func setStructField(value reflect.Value, fieldName string, fieldValue interface{}) {
	field := value.FieldByName(fieldName)
	if !field.IsValid() || !field.CanSet() {
		return
	}

	v := reflect.ValueOf(fieldValue)
	if !v.IsValid() {
		return
	}
	if v.Type().AssignableTo(field.Type()) {
		field.Set(v)
		return
	}
	if v.Type().ConvertibleTo(field.Type()) {
		field.Set(v.Convert(field.Type()))
	}
}

func parsePaneIndex(paneID string) (int, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(paneID, "%"))
	if trimmed == "" {
		return 0, fmt.Errorf("pane id is empty")
	}

	index, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse pane index from %q: %w", paneID, err)
	}

	return index, nil
}
