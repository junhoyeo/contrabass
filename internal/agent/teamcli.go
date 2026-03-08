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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), r.startupTimeout)
	defer cancel()
	if err := r.shutdownTeam(shutdownCtx, state.workspace, state.teamName); err != nil {
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), r.startupTimeout)
		err := r.shutdownTeam(shutdownCtx, proc.workspace, proc.teamName)
		cancel()
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

	var (
		lastPhase      string
		lastTaskStatus string
		seenStarted    bool
		errorCount     int
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

func formatCommandOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}
