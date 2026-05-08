package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

var (
	diffStatCommand        = exec.CommandContext
	diffStatFilesPattern   = regexp.MustCompile(`(\d+)\s+files?\s+changed`)
	diffStatAddedPattern   = regexp.MustCompile(`(\d+)\s+insertions?\(\+\)`)
	diffStatRemovedPattern = regexp.MustCompile(`(\d+)\s+deletions?\(-\)`)
)

// StateSnapshot represents a thread-safe point-in-time copy of orchestrator state.
type StateSnapshot struct {
	Stats       Stats                  `json:"stats"`
	Running     []RunningEntry         `json:"running"`
	Backoff     []types.BackoffEntry   `json:"backoff"`
	Issues      map[string]types.Issue `json:"issues"`
	GeneratedAt time.Time              `json:"generated_at"`
	BuildInfo   BuildInfo              `json:"build_info"`
}

// BuildInfo carries the ldflags-injected version metadata so dashboards and
// API consumers can confirm which binary is currently serving traffic.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// RunningEntry represents a running issue execution in the snapshot.
type RunningEntry struct {
	IssueID          string         `json:"issue_id"`
	Attempt          int            `json:"attempt"`
	PID              int            `json:"pid"`
	SessionID        string         `json:"session_id"`
	Workspace        string         `json:"workspace"`
	StartedAt        time.Time      `json:"started_at"`
	Phase            types.RunPhase `json:"phase"`
	PhaseLabel       string         `json:"phase_label"`
	TokensIn         int64          `json:"tokens_in"`
	TokensOut        int64          `json:"tokens_out"`
	LastActivityAt   string         `json:"last_activity_at"`
	LastActivityKind string         `json:"last_activity_kind"`
	LastHeartbeatAt  string         `json:"last_heartbeat_at"`
	Iteration        int            `json:"iteration"`
	IterationMax     int            `json:"iteration_max"`
	DiffAdded        int            `json:"diff_added"`
	DiffRemoved      int            `json:"diff_removed"`
	DiffFiles        int            `json:"diff_files"`
	DiffStatus       string         `json:"diff_status"`
	AgentStage       string         `json:"agent_stage"`
	AgentStageStep   int            `json:"agent_stage_step"`
	ETACompletionAt  string         `json:"eta_completion_at,omitempty"`
	ETAConfidence    string         `json:"eta_confidence,omitempty"`
}

// Snapshot returns a thread-safe point-in-time copy of orchestrator state.
// The returned snapshot is isolated from the orchestrator's internal state,
// so mutations to the orchestrator after Snapshot() returns do not affect
// the returned snapshot.
func (o *Orchestrator) Snapshot() StateSnapshot {
	o.mu.Lock()

	// Copy stats (value type)
	statsCopy := o.stats

	// Build running entries from running map
	runningEntries := make([]RunningEntry, 0, len(o.running))
	for _, entry := range o.running {
		runningEntries = append(runningEntries, RunningEntry{
			IssueID:          entry.issue.ID,
			Attempt:          entry.attempt.Attempt,
			PID:              entry.attempt.PID,
			SessionID:        entry.attempt.SessionID,
			Workspace:        entry.workspace,
			StartedAt:        entry.attempt.StartTime,
			Phase:            entry.attempt.Phase,
			PhaseLabel:       entry.attempt.Phase.Label(),
			TokensIn:         entry.attempt.TokensIn,
			TokensOut:        entry.attempt.TokensOut,
			LastActivityAt:   formatSnapshotTime(entry.lastActivityAt),
			LastActivityKind: entry.lastActivityKind,
			LastHeartbeatAt:  formatSnapshotTime(entry.lastHeartbeatAt),
			DiffStatus:       "ok",
		})
	}

	// Copy backoff slice
	backoffCopy := make([]types.BackoffEntry, len(o.backoff))
	copy(backoffCopy, o.backoff)

	// Deep-copy issue cache map
	issuesCopy := make(map[string]types.Issue, len(o.issueCache))
	for id, issue := range o.issueCache {
		issuesCopy[id] = issue
	}

	o.mu.Unlock()

	now := time.Now()
	for i := range runningEntries {
		added, removed, files, status := diffStat(context.Background(), runningEntries[i].Workspace)
		runningEntries[i].DiffAdded = added
		runningEntries[i].DiffRemoved = removed
		runningEntries[i].DiffFiles = files
		runningEntries[i].DiffStatus = status
		iteration, max := readIterationProgress(runningEntries[i].Workspace)
		runningEntries[i].Iteration = iteration
		runningEntries[i].IterationMax = max

		// Compute stage + ETA, updating per-run state under the lock.
		issueID := runningEntries[i].IssueID
		tokensTotal := runningEntries[i].TokensIn + runningEntries[i].TokensOut
		startedAt := runningEntries[i].StartedAt
		lastActivityKind := runningEntries[i].LastActivityKind

		var stage string
		var step int
		var etaAt, etaConf string

		o.mu.Lock()
		if e, ok := o.running[issueID]; ok {
			elapsedMin := now.Sub(startedAt).Minutes()
			var tokPerMin float64
			if elapsedMin > 0 {
				tokPerMin = float64(tokensTotal) / elapsedMin
			}
			stage, step = classifyAgentStage(&e.stageState, lastActivityKind, added, removed, tokPerMin, now)
			etaAt, etaConf = estimateCompletionAt(startedAt, now, files, runningEntries[i].TokensIn, runningEntries[i].TokensOut, step, 0)
		}
		o.mu.Unlock()

		runningEntries[i].AgentStage = stage
		runningEntries[i].AgentStageStep = step
		runningEntries[i].ETACompletionAt = etaAt
		runningEntries[i].ETAConfidence = etaConf
	}

	generatedAt := time.Now()

	return StateSnapshot{
		Stats:       statsCopy,
		Running:     runningEntries,
		Backoff:     backoffCopy,
		Issues:      issuesCopy,
		GeneratedAt: generatedAt,
		BuildInfo:   o.buildInfo,
	}
}

// SetBuildInfo records the ldflags-injected build metadata so it is exposed
// via Snapshot() and the /api/v1/state endpoint. Safe to call before Run().
func (o *Orchestrator) SetBuildInfo(info BuildInfo) {
	o.mu.Lock()
	o.buildInfo = info
	o.mu.Unlock()
}

// diffStat runs `git diff --shortstat HEAD` in the workspace and returns
// (added, removed, files, status). status is "ok" / "timeout" / "error".
// On failure the int triple is zeroed and status carries the failure mode.
func diffStat(ctx context.Context, workspace string) (added, removed, files int, status string) {
	if strings.TrimSpace(workspace) == "" {
		return 0, 0, 0, "error"
	}

	cmdCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	output, err := diffStatCommand(cmdCtx, "git", "-C", workspace, "diff", "--shortstat", "HEAD").Output()
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return 0, 0, 0, "timeout"
		}
		return 0, 0, 0, "error"
	}

	added, removed, files = parseDiffShortstat(string(output))
	return added, removed, files, "ok"
}

// readIterationProgress returns iteration progress from an omx run-state file.
// Missing or malformed files return zeroes because non-omx runners do not
// produce this optional state.
func readIterationProgress(workspace string) (iteration, max int) {
	path := filepath.Join(workspace, ".omx", "state", "run-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}

	var state struct {
		Iteration     int `json:"iteration"`
		MaxIterations int `json:"max_iterations"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, 0
	}
	return state.Iteration, state.MaxIterations
}

func parseDiffShortstat(output string) (added, removed, files int) {
	line := strings.TrimSpace(output)
	if line == "" {
		return 0, 0, 0
	}

	files = parseFirstInt(diffStatFilesPattern, line)
	added = parseFirstInt(diffStatAddedPattern, line)
	removed = parseFirstInt(diffStatRemovedPattern, line)
	return added, removed, files
}

func parseFirstInt(pattern *regexp.Regexp, input string) int {
	matches := pattern.FindStringSubmatch(input)
	if len(matches) < 2 {
		return 0
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return value
}

func formatSnapshotTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// agentStageState carries the rolling state classifyAgentStage needs
// across snapshot ticks.
type agentStageState struct {
	LastDiffAdded   int
	LastDiffRemoved int
	LastDiffChange  time.Time
	PrevStep        int
}

// classifyAgentStage returns (stage, step) per the rules in
// design.md Decision 1, then applies the monotonic clamp from
// Decision 2: step is max(state.PrevStep, computed).
func classifyAgentStage(
	state *agentStageState,
	lastActivityKind string,
	diffAdded, diffRemoved int,
	tokensPerMin float64,
	now time.Time,
) (string, int) {
	var stage string
	var step int

	switch lastActivityKind {
	case "turn/completed", "turn/failed", "turn/cancelled":
		stage, step = "Wrapping", 5
	default:
		elapsed := now.Sub(state.LastDiffChange)
		diffChanged := diffAdded > state.LastDiffAdded || diffRemoved > state.LastDiffRemoved
		diffPlateaued := diffAdded == 0 && diffRemoved == 0

		switch {
		case diffPlateaued && elapsed > 60*time.Second && tokensPerMin < 50_000:
			stage, step = "Reviewing", 4
		case diffPlateaued && elapsed > 30*time.Second:
			stage, step = "Testing", 3
		case diffChanged:
			stage, step = "Editing", 2
		default:
			stage, step = "Exploration", 1
		}
	}

	// Monotonic clamp: step never goes backwards.
	if step < state.PrevStep {
		step = state.PrevStep
		switch step {
		case 1:
			stage = "Exploration"
		case 2:
			stage = "Editing"
		case 3:
			stage = "Testing"
		case 4:
			stage = "Reviewing"
		case 5:
			stage = "Wrapping"
		}
	}

	// Update state for next tick.
	if diffAdded > state.LastDiffAdded || diffRemoved > state.LastDiffRemoved {
		state.LastDiffAdded = diffAdded
		state.LastDiffRemoved = diffRemoved
		state.LastDiffChange = now
	}
	state.PrevStep = step

	return stage, step
}

// estimateCompletionAt returns (rfc3339, confidence) per design.md
// Decision 3. Empty rfc3339 means "do not display a timestamp"; the
// confidence string is still set so the dashboard can show the
// elapsed-text fallback.
func estimateCompletionAt(
	startedAt time.Time,
	now time.Time,
	diffFiles int,
	tokensIn, tokensOut int64,
	stageStep int,
	estimatedTotalFiles int,
) (string, string) {
	elapsedMin := now.Sub(startedAt).Minutes()

	if elapsedMin < 3 {
		return "", "low"
	}

	filesPerMin := float64(diffFiles) / elapsedMin
	tokensPerMin := float64(tokensIn+tokensOut) / elapsedMin

	if filesPerMin < 0.05 || tokensPerMin < 1000 {
		return "", "low"
	}

	// Determine confidence band.
	var confidence string
	if elapsedMin > 8 && stageStep >= 3 {
		confidence = "high"
	} else if elapsedMin > 5 {
		confidence = "medium"
	} else {
		confidence = "low"
	}

	if confidence == "low" {
		return "", "low"
	}

	// Compute ETA.
	currentFiles := diffFiles
	if estimatedTotalFiles <= currentFiles {
		estimated := float64(currentFiles) * 1.2
		if estimated < 11 {
			estimated = 11
		}
		estimatedTotalFiles = int(estimated)
	}

	var linearRemainingMin float64
	if estimatedTotalFiles > currentFiles {
		linearRemainingMin = float64(estimatedTotalFiles-currentFiles) / filesPerMin
	} else {
		linearRemainingMin = 5
	}

	totalEstimateMin := elapsedMin + linearRemainingMin*1.35
	etaAt := startedAt.Add(time.Duration(totalEstimateMin * float64(time.Minute)))

	return etaAt.UTC().Format(time.RFC3339), confidence
}
