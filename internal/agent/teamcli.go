package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/types"
)

var (
	errTeamCLIAlreadyStopped = errors.New("team CLI process already stopped")
	errTeamCLIStopFailed     = errors.New("team CLI stop failed")
)

type teamCLIRunner struct {
	name           string
	binaryPath     string
	teamSpec       string
	pollInterval   time.Duration
	startupTimeout time.Duration
	startArgs      func(teamSpec, task string) []string
	shutdownArgs   func(teamName string) []string
	teamName       func(taskSeed string) string
	logger         *log.Logger

	pidSeq atomic.Int64

	mu    sync.Mutex
	procs map[int]*teamCLIProcess
}

type teamCLIProcess struct {
	pid        int
	teamName   string
	workspace  string
	promptPath string
	events     chan types.AgentEvent
	done       chan error
	finished   chan struct{}
	cancel     context.CancelFunc
	finishOnce sync.Once
	removeOnce sync.Once
}

type teamCLIEnvelope struct {
	OK        bool            `json:"ok"`
	Operation string          `json:"operation"`
	Data      json.RawMessage `json:"data"`
	Error     *teamCLIError   `json:"error"`
}

type teamCLIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *teamCLIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" && e.Message != "" {
		return e.Code + ": " + e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return e.Message
}

type teamSummaryResponse struct {
	Summary teamSummary `json:"summary"`
}

type teamTasksResponse struct {
	Tasks []teamTask `json:"tasks"`
}

type teamSummary struct {
	TeamName            string                 `json:"teamName"`
	WorkerCount         int                    `json:"workerCount"`
	Tasks               teamSummaryTaskCounts  `json:"tasks"`
	Workers             []teamSummaryWorker    `json:"workers"`
	NonReportingWorkers []string               `json:"nonReportingWorkers"`
	Performance         map[string]interface{} `json:"performance,omitempty"`
}

type teamSummaryTaskCounts struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Blocked    int `json:"blocked"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
}

type teamSummaryWorker struct {
	Name                 string `json:"name"`
	Alive                bool   `json:"alive"`
	LastTurnAt           string `json:"lastTurnAt"`
	TurnsWithoutProgress int    `json:"turnsWithoutProgress"`
}

type teamTask struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	Owner       string `json:"owner,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type teamSnapshot struct {
	Summary teamSummary
	Tasks   []teamTask
}

func newTeamCLIRunner(cfg *teamCLIRunner) *teamCLIRunner {
	if cfg.pollInterval <= 0 {
		cfg.pollInterval = time.Second
	}
	if cfg.startupTimeout <= 0 {
		cfg.startupTimeout = 15 * time.Second
	}
	if cfg.teamSpec == "" {
		cfg.teamSpec = "1:executor"
	}
	if cfg.teamName == nil {
		cfg.teamName = slugifyTeamName
	}
	if cfg.startArgs == nil {
		cfg.startArgs = func(teamSpec, task string) []string {
			return []string{"team", teamSpec, task}
		}
	}
	if cfg.shutdownArgs == nil {
		cfg.shutdownArgs = func(teamName string) []string {
			return []string{"team", "shutdown", teamName, "--force"}
		}
	}
	if cfg.logger == nil {
		cfg.logger = log.NewWithOptions(io.Discard, log.Options{})
	}
	cfg.procs = make(map[int]*teamCLIProcess)
	return cfg
}

func (p *teamCLIProcess) finish(err error) {
	p.finishOnce.Do(func() {
		p.done <- err
		close(p.done)
		close(p.finished)
	})
}

func (p *teamCLIProcess) remove(r *teamCLIRunner) {
	p.removeOnce.Do(func() {
		r.mu.Lock()
		delete(r.procs, p.pid)
		r.mu.Unlock()
	})
}

func (r *teamCLIRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	if strings.TrimSpace(r.binaryPath) == "" {
		return nil, fmt.Errorf("%s binary path is empty", r.name)
	}

	taskSeed := buildTeamTaskSeed(issue, prompt)
	promptPath, relPromptPath, err := writeTeamPromptFile(workspace, r.name, issue, taskSeed, prompt)
	if err != nil {
		return nil, fmt.Errorf("write %s prompt file: %w", r.name, err)
	}

	launchTask := buildLaunchTask(taskSeed, relPromptPath)
	teamName := r.teamName(launchTask)
	startCtx, cancel := context.WithTimeout(ctx, r.startupTimeout)
	defer cancel()

	if output, err := r.runCommand(startCtx, workspace, r.startArgs(r.teamSpec, launchTask)...); err != nil {
		return nil, fmt.Errorf("start %s team %q: %w%s", r.name, teamName, err, formatCommandOutput(output))
	}

	initialSnapshot, err := r.waitForTeamReady(startCtx, workspace, teamName)
	if err != nil {
		_ = r.shutdownTeam(context.Background(), workspace, teamName)
		return nil, fmt.Errorf("wait for %s team %q readiness: %w", r.name, teamName, err)
	}

	pid := int(r.pidSeq.Add(1))
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	state := &teamCLIProcess{
		pid:        pid,
		teamName:   teamName,
		workspace:  workspace,
		promptPath: promptPath,
		events:     make(chan types.AgentEvent, 128),
		done:       make(chan error, 1),
		finished:   make(chan struct{}),
		cancel:     monitorCancel,
	}

	r.mu.Lock()
	r.procs[pid] = state
	r.mu.Unlock()

	go r.monitorProcess(monitorCtx, state, initialSnapshot)
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Stop(&AgentProcess{PID: pid, SessionID: teamName})
		case <-state.finished:
		}
	}()

	return &AgentProcess{
		PID:       pid,
		SessionID: teamName,
		Events:    state.events,
		Done:      state.done,
		serverURL: promptPath,
	}, nil
}

func (r *teamCLIRunner) Stop(proc *AgentProcess) error {
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
		return fmt.Errorf("%w: pid %d", errTeamCLIAlreadyStopped, proc.PID)
	}

	state.cancel()
	state.remove(r)

	// Try graceful shutdown first with a short budget, then force-stop
	// with its own independent timeout so an expired grace period cannot
	// starve the force-shutdown.
	graceCtx, graceCancel := context.WithTimeout(context.Background(), r.startupTimeout/2)
	r.broadcastShutdownNotice(graceCtx, state.workspace, state.teamName)
	r.gracefulShutdownWorkers(graceCtx, state.workspace, state.teamName)
	graceCancel()

	forceCtx, forceCancel := context.WithTimeout(context.Background(), r.startupTimeout)
	defer forceCancel()

	if err := r.shutdownTeam(forceCtx, state.workspace, state.teamName); err != nil {
		return fmt.Errorf("%w: %v", errTeamCLIStopFailed, err)
	}

	return nil
}

func (r *teamCLIRunner) Close() error {
	r.mu.Lock()
	states := make([]*teamCLIProcess, 0, len(r.procs))
	for _, proc := range r.procs {
		states = append(states, proc)
	}
	r.procs = make(map[int]*teamCLIProcess)
	r.mu.Unlock()

	var errs []error
	for _, proc := range states {
		proc.cancel()
		proc.remove(r)
		graceCtx, graceCancel := context.WithTimeout(context.Background(), r.startupTimeout/2)
		r.broadcastShutdownNotice(graceCtx, proc.workspace, proc.teamName)
		r.gracefulShutdownWorkers(graceCtx, proc.workspace, proc.teamName)
		graceCancel()
		forceCtx, forceCancel := context.WithTimeout(context.Background(), r.startupTimeout)
		err := r.shutdownTeam(forceCtx, proc.workspace, proc.teamName)
		forceCancel()
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (r *teamCLIRunner) waitForTeamReady(ctx context.Context, workspace string, teamName string) (*teamSnapshot, error) {
	for {
		snapshot, err := r.fetchSnapshot(ctx, workspace, teamName)
		if err == nil {
			return snapshot, nil
		}

		var apiErr *teamCLIError
		if !errors.As(err, &apiErr) || (apiErr.Code != "team_not_found" && apiErr.Code != "manifest_not_found") {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *teamCLIRunner) monitorProcess(ctx context.Context, proc *teamCLIProcess, initial *teamSnapshot) {
	defer close(proc.events)
	defer proc.remove(r)

	emit := func(eventType string, data map[string]interface{}) {
		event := types.AgentEvent{Type: eventType, Data: data, Timestamp: time.Now()}
		select {
		case <-ctx.Done():
		case proc.events <- event:
		}
	}

	finish := func(err error) {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), r.startupTimeout)
		defer cancel()
		if shutdownErr := r.shutdownTeam(shutdownCtx, proc.workspace, proc.teamName); shutdownErr != nil {
			emit("protocol/error", map[string]interface{}{
				"team_name": proc.teamName,
				"error":     shutdownErr.Error(),
			})
		}
		proc.finish(err)
	}

	emit("turn/started", map[string]interface{}{
		"team_name":   proc.teamName,
		"prompt_file": proc.promptPath,
		"runner":      r.name,
	})

	const healthCheckInterval = 5 // check health every N poll cycles
	var (
		lastPhase      string
		lastTaskStatus string
		seenStarted    bool
		errorCount     int
		pollCount      int
	)

	handleSnapshot := func(snapshot *teamSnapshot) (bool, error) {
		phase := summarizePhase(snapshot.Summary)
		if phase != lastPhase {
			emit("session.status", map[string]interface{}{
				"team_name": proc.teamName,
				"phase":     phase,
				"summary":   mustJSONMap(snapshot.Summary),
			})
			lastPhase = phase
		}

		task, ok := primaryTask(snapshot.Tasks)
		if !ok {
			return false, nil
		}

		if task.Status == "in_progress" && !seenStarted {
			emit("item/started", map[string]interface{}{
				"team_name": proc.teamName,
				"task":      mustJSONMap(task),
			})
			seenStarted = true

			if task.Owner != "" {
				if _, sendErr := r.SendMessage(ctx, proc.workspace, proc.teamName, "coordinator", task.Owner,
					fmt.Sprintf("Task %s is now in progress. Good luck!", task.ID)); sendErr != nil {
					r.logger.Warn("failed to send task-start notification", "team", proc.teamName, "task", task.ID, "worker", task.Owner, "error", sendErr)
				}
			}
		}

		if task.Status == lastTaskStatus {
			return false, nil
		}
		lastTaskStatus = task.Status

		switch task.Status {
		case "pending", "blocked", "in_progress":
			return false, nil
		case "completed":
			emit("item/completed", map[string]interface{}{
				"team_name": proc.teamName,
				"task":      mustJSONMap(task),
				"result":    firstNonEmpty(task.Result, task.Description),
			})
			emit("task/completed", map[string]interface{}{
				"team_name": proc.teamName,
				"task":      mustJSONMap(task),
				"result":    firstNonEmpty(task.Result, task.Description),
			})
			return true, nil
		case "failed":
			message := firstNonEmpty(task.Error, task.Result, "task failed")
			emit("turn/failed", map[string]interface{}{
				"team_name": proc.teamName,
				"task":      mustJSONMap(task),
				"error":     message,
			})
			return true, errors.New(message)
		default:
			emit("protocol/error", map[string]interface{}{
				"team_name": proc.teamName,
				"error":     "unknown task status",
				"status":    task.Status,
			})
			return true, fmt.Errorf("unknown task status %q", task.Status)
		}
	}

	if initial != nil {
		if done, err := handleSnapshot(initial); done {
			finish(err)
			return
		}
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	var lastEventID string

	for {
		select {
		case <-ctx.Done():
			proc.finish(nil)
			return
		case <-ticker.C:
			snapshot, err := r.fetchSnapshot(ctx, proc.workspace, proc.teamName)
			if err != nil {
				errorCount++
				emit("protocol/error", map[string]interface{}{
					"team_name": proc.teamName,
					"error":     err.Error(),
				})
				if errorCount >= 3 {
					finish(err)
					return
				}
				continue
			}
			errorCount = 0
			if done, err := handleSnapshot(snapshot); done {
				finish(err)
				return
			}

			pollCount++
			if pollCount%healthCheckInterval == 0 {
				r.checkTeamHealthAndStall(ctx, proc, emit)
			}

			r.awaitNextEvent(ctx, proc, &lastEventID, emit)
		}
	}
}

func (r *teamCLIRunner) fetchSnapshot(ctx context.Context, workspace, teamName string) (*teamSnapshot, error) {
	var summaryResp teamSummaryResponse
	if err := r.runTeamAPI(ctx, workspace, "get-summary", map[string]string{"team_name": teamName}, &summaryResp); err != nil {
		return nil, err
	}

	var tasksResp teamTasksResponse
	if err := r.runTeamAPI(ctx, workspace, "list-tasks", map[string]string{"team_name": teamName}, &tasksResp); err != nil {
		return nil, err
	}

	return &teamSnapshot{Summary: summaryResp.Summary, Tasks: tasksResp.Tasks}, nil
}

func (r *teamCLIRunner) shutdownTeam(ctx context.Context, workspace, teamName string) error {
	output, err := r.runCommand(ctx, workspace, r.shutdownArgs(teamName)...)
	if err == nil {
		return nil
	}
	if strings.Contains(string(output), "No team state found") {
		return nil
	}
	return fmt.Errorf("shutdown %s team %q: %w%s", r.name, teamName, err, formatCommandOutput(output))
}

func (r *teamCLIRunner) runTeamAPI(ctx context.Context, workspace, operation string, input interface{}, target interface{}) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal team api input: %w", err)
	}

	output, err := r.runCommand(ctx, workspace, "team", "api", operation, "--input", string(payload), "--json")
	if err != nil {
		return fmt.Errorf("run %s team api %s: %w%s", r.name, operation, err, formatCommandOutput(output))
	}

	var envelope teamCLIEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil {
		return fmt.Errorf("decode %s team api %s response: %w", r.name, operation, err)
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return envelope.Error
		}
		return fmt.Errorf("%s team api %s returned ok=false", r.name, operation)
	}
	if target == nil || len(envelope.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("decode %s team api %s payload: %w", r.name, operation, err)
	}
	return nil
}

func (r *teamCLIRunner) runCommand(ctx context.Context, workspace string, args ...string) ([]byte, error) {
	argv := strings.Fields(strings.TrimSpace(r.binaryPath))
	if len(argv) == 0 {
		return nil, fmt.Errorf("%s binary path is empty", r.name)
	}

	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], args...)...)
	cmd.Dir = workspace
	return cmd.CombinedOutput()
}

func buildTeamTaskSeed(issue types.Issue, prompt string) string {
	seed := strings.TrimSpace(strings.TrimSpace(issue.ID) + " " + strings.TrimSpace(issue.Title))
	if seed != "" {
		return seed
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "team-task"
	}
	line := strings.SplitN(prompt, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if line == "" {
		line = prompt
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

func buildLaunchTask(taskSeed, relPromptPath string) string {
	return fmt.Sprintf(
		"%s. Read and execute the detailed task instructions in %s. Report completion through the team runtime.",
		strings.TrimSpace(taskSeed),
		relPromptPath,
	)
}

func writeTeamPromptFile(workspace, runnerName string, issue types.Issue, taskSeed, prompt string) (string, string, error) {
	fileSlug := slugifyTeamName(firstNonEmpty(issue.ID, issue.Identifier, taskSeed))
	relPath := filepath.ToSlash(filepath.Join(".contrabass", "runner", runnerName, fileSlug+"-task.md"))
	absPath := filepath.Join(workspace, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("# Contrabass Task\n\n")
	if issue.ID != "" {
		b.WriteString("- Issue ID: ")
		b.WriteString(issue.ID)
		b.WriteString("\n")
	}
	if issue.Title != "" {
		b.WriteString("- Issue Title: ")
		b.WriteString(issue.Title)
		b.WriteString("\n")
	}
	if issue.Identifier != "" {
		b.WriteString("- Issue Identifier: ")
		b.WriteString(issue.Identifier)
		b.WriteString("\n")
	}
	if issue.URL != "" {
		b.WriteString("- Issue URL: ")
		b.WriteString(issue.URL)
		b.WriteString("\n")
	}
	b.WriteString("\n## Instructions\n\n")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n")

	if err := os.WriteFile(absPath, []byte(b.String()), 0o644); err != nil {
		return "", "", err
	}
	return absPath, relPath, nil
}

func slugifyTeamName(input string) string {
	slug := strings.ToLower(input)
	slug = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, slug)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 30 {
		slug = strings.TrimRight(slug[:30], "-")
	}
	if slug == "" {
		return "team-task"
	}
	return slug
}

func summarizePhase(summary teamSummary) string {
	switch {
	case summary.Tasks.Failed > 0:
		return "failed"
	case summary.Tasks.Total > 0 && summary.Tasks.Completed == summary.Tasks.Total:
		return "completed"
	case summary.Tasks.InProgress > 0:
		return "in_progress"
	case summary.Tasks.Blocked > 0:
		return "blocked"
	default:
		return "pending"
	}
}

func primaryTask(tasks []teamTask) (teamTask, bool) {
	if len(tasks) == 0 {
		return teamTask{}, false
	}
	best := tasks[0]
	for _, task := range tasks[1:] {
		bestNum, bestErr := strconv.Atoi(best.ID)
		taskNum, taskErr := strconv.Atoi(task.ID)
		if bestErr == nil && taskErr == nil {
			if taskNum < bestNum {
				best = task
			}
			continue
		}
		if task.ID < best.ID {
			best = task
		}
	}
	return best, true
}

func mustJSONMap(v interface{}) map[string]interface{} {
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return m
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// checkTeamHealthAndStall runs health and stall detection during monitoring,
// emitting events for dead or stalled workers so the operator can react.
func (r *teamCLIRunner) checkTeamHealthAndStall(ctx context.Context, proc *teamCLIProcess, emit func(string, map[string]interface{})) {
	const maxHeartbeatAge = 60 * time.Second

	stallState, err := r.ReadStallState(ctx, proc.workspace, proc.teamName)
	if err != nil {
		r.logger.Warn("stall state check failed", "team", proc.teamName, "error", err)
		return
	}

	r.nudgeIdleWorkers(ctx, proc, stallState, emit)
	r.checkIdleState(ctx, proc, emit)
	r.processUnreadMessages(ctx, proc, emit)

	if stallState.TeamStalled {
		r.handleStalledTeam(ctx, proc, stallState, maxHeartbeatAge, emit)
	}

	r.checkWorkerInterventions(ctx, proc, maxHeartbeatAge, emit)
	r.reconcileTaskStates(ctx, proc, emit)
}

func (r *teamCLIRunner) checkIdleState(ctx context.Context, proc *teamCLIProcess, emit func(string, map[string]interface{})) {
	idleState, err := r.ReadIdleState(ctx, proc.workspace, proc.teamName)
	if err != nil {
		r.logger.Warn("idle state check failed", "team", proc.teamName, "error", err)
		return
	}

	if idleState.AllWorkersIdle {
		emit("team/all_idle", map[string]interface{}{
			"team_name":    proc.teamName,
			"worker_count": idleState.WorkerCount,
			"idle_workers": idleState.IdleWorkers,
		})
	}
}

func (r *teamCLIRunner) processUnreadMessages(ctx context.Context, proc *teamCLIProcess, emit func(string, map[string]interface{})) {
	messages, err := r.GetUnreadMessages(ctx, proc.workspace, proc.teamName, "coordinator")
	if err != nil {
		r.logger.Warn("failed to read unread messages", "team", proc.teamName, "error", err)
		return
	}

	for _, msg := range messages {
		emit("message/received", map[string]interface{}{
			"team_name":  proc.teamName,
			"message_id": msg.ID,
			"from":       msg.From,
			"content":    msg.Content,
		})

		if err := r.MarkMessageDelivered(ctx, proc.workspace, proc.teamName, "coordinator", msg.ID); err != nil {
			r.logger.Warn("failed to mark message delivered", "team", proc.teamName, "message_id", msg.ID, "error", err)
			continue
		}
		if err := r.MarkMessageNotified(ctx, proc.workspace, proc.teamName, "coordinator", msg.ID); err != nil {
			r.logger.Warn("failed to mark message notified", "team", proc.teamName, "message_id", msg.ID, "error", err)
		}
	}
}

func (r *teamCLIRunner) handleStalledTeam(ctx context.Context, proc *teamCLIProcess, stallState *StallState, maxHeartbeatAge time.Duration, emit func(string, map[string]interface{})) {
	recentEvents, err := r.ReadEvents(ctx, proc.workspace, proc.teamName, &EventFilter{})
	if err != nil {
		r.logger.Warn("failed to read recent events for stall diagnostics", "team", proc.teamName, "error", err)
	}

	stallData := map[string]interface{}{
		"team_name":       proc.teamName,
		"reasons":         stallState.Reasons,
		"dead_workers":    stallState.DeadWorkers,
		"stalled_workers": stallState.StalledWorkers,
		"pending_tasks":   stallState.PendingTaskCount,
	}
	if err == nil && len(recentEvents) > 0 {
		last := recentEvents[len(recentEvents)-1]
		stallData["last_event_type"] = last.Type
		stallData["last_event_time"] = last.Timestamp.Format(time.RFC3339)
	}
	emit("team/stalled", stallData)

	if len(stallState.DeadWorkers) > 0 {
		results, restartErr := r.RestartDeadWorkers(ctx, proc.workspace, proc.teamName, maxHeartbeatAge)
		if restartErr != nil {
			r.logger.Warn("failed to restart dead workers", "team", proc.teamName, "error", restartErr)
		} else {
			for _, result := range results {
				emit("worker/restarted", map[string]interface{}{
					"team_name":        proc.teamName,
					"worker":           result.WorkerName,
					"success":          result.Success,
					"reassigned_tasks": result.ReassignedTasks,
				})
				if !result.Success {
					r.createSubtask(ctx, proc,
						fmt.Sprintf("Recover from dead worker %s", result.WorkerName),
						fmt.Sprintf("Worker %s died and restart failed: %s. Reassign its tasks and resume work.", result.WorkerName, result.Error),
						emit,
					)
				}
			}
		}
	}
}

func (r *teamCLIRunner) checkWorkerInterventions(ctx context.Context, proc *teamCLIProcess, maxHeartbeatAge time.Duration, emit func(string, map[string]interface{})) {
	health, err := r.GetTeamHealth(ctx, proc.workspace, proc.teamName, maxHeartbeatAge)
	if err != nil {
		r.logger.Warn("team health check failed", "team", proc.teamName, "error", err)
		return
	}

	for _, report := range health.WorkerReports {
		reason, checkErr := r.CheckWorkerNeedsIntervention(ctx, proc.workspace, proc.teamName, report.WorkerName, maxHeartbeatAge)
		if checkErr != nil {
			r.logger.Warn("worker intervention check failed", "team", proc.teamName, "worker", report.WorkerName, "error", checkErr)
			continue
		}
		if reason == "" {
			continue
		}

		emit("worker/needs_intervention", map[string]interface{}{
			"team_name": proc.teamName,
			"worker":    report.WorkerName,
			"reason":    reason,
		})

		if report.ConsecutiveErrors >= 3 {
			if qErr := r.QuarantineWorker(ctx, proc.workspace, proc.teamName, report.WorkerName, reason); qErr != nil {
				r.logger.Warn("failed to quarantine worker", "team", proc.teamName, "worker", report.WorkerName, "error", qErr)
			} else {
				emit("worker/quarantined", map[string]interface{}{
					"team_name": proc.teamName,
					"worker":    report.WorkerName,
					"reason":    reason,
				})
			}
		}
	}
}

func (r *teamCLIRunner) reconcileTaskStates(ctx context.Context, proc *teamCLIProcess, emit func(string, map[string]interface{})) {
	pendingTasks, err := r.GetTasksByStatus(ctx, proc.workspace, proc.teamName, "pending")
	if err != nil {
		r.logger.Warn("failed to list pending tasks", "team", proc.teamName, "error", err)
		return
	}
	if len(pendingTasks) == 0 {
		return
	}

	health, err := r.GetTeamHealth(ctx, proc.workspace, proc.teamName, 60*time.Second)
	if err != nil {
		return
	}

	var idleWorkers []string
	for _, report := range health.WorkerReports {
		if report.IsAlive && report.Status == "idle" && report.CurrentTaskID == "" {
			idleWorkers = append(idleWorkers, report.WorkerName)
		}
	}

	for i, task := range pendingTasks {
		if i >= len(idleWorkers) {
			break
		}
		worker := idleWorkers[i]
		claimResult, claimErr := r.ClaimTask(ctx, proc.workspace, proc.teamName, task.ID, worker, nil)
		if claimErr != nil {
			r.logger.Warn("failed to claim task for idle worker", "team", proc.teamName, "task", task.ID, "worker", worker, "error", claimErr)
			continue
		}
		if claimResult.OK {
			if _, transErr := r.TransitionTaskStatus(ctx, proc.workspace, proc.teamName, task.ID, "pending", "in_progress", claimResult.ClaimToken, nil, nil); transErr != nil {
				r.logger.Warn("failed to transition task to in_progress", "team", proc.teamName, "task", task.ID, "error", transErr)
				// Release the claim so other workers can pick up the task.
				if _, relErr := r.ReleaseTaskClaim(ctx, proc.workspace, proc.teamName, task.ID, claimResult.ClaimToken, worker); relErr != nil {
					r.logger.Warn("failed to release claim after transition failure", "team", proc.teamName, "task", task.ID, "error", relErr)
				}
				continue
			}

			if _, sendErr := r.SendMessage(ctx, proc.workspace, proc.teamName, "coordinator", worker,
				fmt.Sprintf("Task %s (%s) has been assigned to you.", task.ID, task.Subject)); sendErr != nil {
				r.logger.Warn("failed to notify worker of task assignment", "team", proc.teamName, "task", task.ID, "worker", worker, "error", sendErr)
			}
			emit("task/assigned", map[string]interface{}{
				"team_name": proc.teamName,
				"task_id":   task.ID,
				"worker":    worker,
			})
		}
	}

	r.releaseExpiredClaims(ctx, proc, emit)
}

func (r *teamCLIRunner) releaseExpiredClaims(ctx context.Context, proc *teamCLIProcess, emit func(string, map[string]interface{})) {
	inProgressTasks, err := r.GetTasksByStatus(ctx, proc.workspace, proc.teamName, "in_progress")
	if err != nil {
		return
	}

	health, err := r.GetTeamHealth(ctx, proc.workspace, proc.teamName, 60*time.Second)
	if err != nil {
		return
	}

	deadWorkerSet := make(map[string]bool)
	for _, report := range health.WorkerReports {
		if !report.IsAlive {
			deadWorkerSet[report.WorkerName] = true
		}
	}

	for _, task := range inProgressTasks {
		if task.Claim == nil {
			continue
		}
		if !deadWorkerSet[task.Claim.WorkerID] {
			continue
		}

		if _, err := r.ReleaseTaskClaim(ctx, proc.workspace, proc.teamName, task.ID, task.Claim.Token, task.Claim.WorkerID); err != nil {
			r.logger.Warn("failed to release expired claim", "team", proc.teamName, "task", task.ID, "worker", task.Claim.WorkerID, "error", err)
			continue
		}

		updates := map[string]interface{}{"status": "pending"}
		if _, err := r.UpdateTask(ctx, proc.workspace, proc.teamName, task.ID, updates); err != nil {
			r.logger.Warn("failed to reset task to pending", "team", proc.teamName, "task", task.ID, "error", err)
		}

		emit("task/claim_released", map[string]interface{}{
			"team_name": proc.teamName,
			"task_id":   task.ID,
			"worker":    task.Claim.WorkerID,
			"reason":    "worker_dead",
		})
	}
}

func (r *teamCLIRunner) createSubtask(ctx context.Context, proc *teamCLIProcess, subject, description string, emit func(string, map[string]interface{})) {
	task := &types.TeamTask{
		Subject:     subject,
		Description: description,
	}

	created, err := r.CreateTask(ctx, proc.workspace, proc.teamName, task)
	if err != nil {
		r.logger.Warn("failed to create subtask", "team", proc.teamName, "subject", subject, "error", err)
		return
	}

	emit("task/created", map[string]interface{}{
		"team_name": proc.teamName,
		"task_id":   created.ID,
		"subject":   created.Subject,
	})
}

// gracefulShutdownWorkers sends shutdown requests to all known workers before
// force-stopping the team. Called from Stop/Close for cleaner teardown.
func (r *teamCLIRunner) gracefulShutdownWorkers(ctx context.Context, workspace, teamName string) {
	snapshot, err := r.fetchSnapshot(ctx, workspace, teamName)
	if err != nil {
		return
	}

	for _, worker := range snapshot.Summary.Workers {
		if worker.Alive {
			if writeErr := r.writeShutdownRequest(ctx, workspace, teamName, worker.Name, "team-stop"); writeErr != nil {
				r.logger.Warn("failed to send shutdown request",
					"team", teamName,
					"worker", worker.Name,
					"error", writeErr,
				)
			}
		}
	}

	// Brief grace period for workers to acknowledge.
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
	}
}

// broadcastShutdownNotice sends a shutdown notice to all workers via the
// messaging API so they can wrap up gracefully before the force-stop.
func (r *teamCLIRunner) broadcastShutdownNotice(ctx context.Context, workspace, teamName string) {
	if _, err := r.BroadcastMessage(ctx, workspace, teamName, "coordinator",
		"Team is shutting down. Finish current work and prepare to stop."); err != nil {
		r.logger.Warn("failed to broadcast shutdown notice", "team", teamName, "error", err)
	}
}

// nudgeIdleWorkers broadcasts a nudge to idle workers when pending tasks exist,
// prompting them to pick up available work.
func (r *teamCLIRunner) nudgeIdleWorkers(ctx context.Context, proc *teamCLIProcess, stallState *StallState, emit func(string, map[string]interface{})) {
	if len(stallState.IdleWorkers) == 0 || stallState.PendingTaskCount == 0 {
		return
	}

	emit("workers/idle_nudge", map[string]interface{}{
		"team_name":     proc.teamName,
		"idle_workers":  stallState.IdleWorkers,
		"pending_tasks": stallState.PendingTaskCount,
	})

	body := fmt.Sprintf("There are %d pending tasks available. Please pick up work.", stallState.PendingTaskCount)
	if _, err := r.BroadcastMessage(ctx, proc.workspace, proc.teamName, "coordinator", body); err != nil {
		r.logger.Warn("failed to nudge idle workers", "team", proc.teamName, "error", err)
	}
}

func (r *teamCLIRunner) awaitNextEvent(ctx context.Context, proc *teamCLIProcess, lastEventID *string, emit func(string, map[string]interface{})) {
	filter := &EventFilter{}
	if *lastEventID != "" {
		filter.AfterEventID = *lastEventID
	}

	event, err := r.AwaitEvent(ctx, proc.workspace, proc.teamName, filter, 500*time.Millisecond)
	if err != nil {
		return
	}

	if eventID, ok := event.Data["event_id"].(string); ok && eventID != "" {
		*lastEventID = eventID
	}
	emit("team/event", map[string]interface{}{
		"team_name":  proc.teamName,
		"event_type": event.Type,
		"data":       event.Data,
	})
}

func formatCommandOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}
