package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestPhaseLabel_Coverage(t *testing.T) {
	phases := []types.RunPhase{
		types.PreparingWorkspace,
		types.BuildingPrompt,
		types.LaunchingAgentProcess,
		types.InitializingSession,
		types.StreamingTurn,
		types.Finishing,
		types.Succeeded,
		types.Failed,
		types.TimedOut,
		types.Stalled,
		types.CanceledByReconciliation,
	}

	for _, phase := range phases {
		t.Run(phase.String(), func(t *testing.T) {
			assert.NotEmpty(t, phase.Label())
		})
	}
	assert.Empty(t, types.RunPhase(999).Label())
}

func TestSnapshot_ReturnsCorrectStats(t *testing.T) {
	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats: Stats{
			Running:        5,
			MaxAgents:      10,
			TotalTokensIn:  1000,
			TotalTokensOut: 2000,
			StartTime:      time.Now().Add(-1 * time.Hour),
			PollCount:      42,
		},
	}

	snapshot := o.Snapshot()

	assert.Equal(t, 5, snapshot.Stats.Running)
	assert.Equal(t, 10, snapshot.Stats.MaxAgents)
	assert.Equal(t, int64(1000), snapshot.Stats.TotalTokensIn)
	assert.Equal(t, int64(2000), snapshot.Stats.TotalTokensOut)
	assert.Equal(t, 42, snapshot.Stats.PollCount)
	assert.NotZero(t, snapshot.GeneratedAt)
}

func TestSnapshot_IncludesRunningEntries(t *testing.T) {
	now := time.Now()
	issue := types.Issue{
		ID:    "issue-1",
		Title: "Test Issue",
	}
	attempt := types.RunAttempt{
		IssueID:   "issue-1",
		Attempt:   1,
		PID:       12345,
		SessionID: "session-abc",
		StartTime: now,
		Phase:     types.StreamingTurn,
		TokensIn:  100,
		TokensOut: 200,
	}
	entry := &runEntry{
		issue:     issue,
		attempt:   attempt,
		workspace: "/tmp/workspace",
	}

	o := &Orchestrator{
		running:    map[string]*runEntry{"issue-1": entry},
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{},
	}

	snapshot := o.Snapshot()

	require.Len(t, snapshot.Running, 1)
	assert.Equal(t, "issue-1", snapshot.Running[0].IssueID)
	assert.Equal(t, 1, snapshot.Running[0].Attempt)
	assert.Equal(t, 12345, snapshot.Running[0].PID)
	assert.Equal(t, "session-abc", snapshot.Running[0].SessionID)
	assert.Equal(t, "/tmp/workspace", snapshot.Running[0].Workspace)
	assert.Equal(t, now, snapshot.Running[0].StartedAt)
	assert.Equal(t, types.StreamingTurn, snapshot.Running[0].Phase)
	assert.Equal(t, int64(100), snapshot.Running[0].TokensIn)
	assert.Equal(t, int64(200), snapshot.Running[0].TokensOut)
}

func TestSnapshot_LastActivityIgnoresHeartbeats(t *testing.T) {
	orch := NewOrchestrator(nil, nil, nil, nil, nil)
	issueID := "ISS-ACTIVITY"
	toolCallAt := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	heartbeatAt := toolCallAt

	orch.mu.Lock()
	orch.running[issueID] = &runEntry{
		issue:     types.Issue{ID: issueID},
		attempt:   types.RunAttempt{IssueID: issueID, Attempt: 1, Phase: types.StreamingTurn},
		workspace: t.TempDir(),
	}
	orch.stats.Running = 1
	orch.mu.Unlock()

	orch.handleAgentEvent(issueID, types.AgentEvent{Type: "tool_call", Timestamp: toolCallAt})
	for i := 1; i <= 5; i++ {
		heartbeatAt = toolCallAt.Add(time.Duration(i) * time.Second)
		orch.handleAgentEvent(issueID, types.AgentEvent{Type: "team/stalled", Timestamp: heartbeatAt})
	}

	snapshot := orch.Snapshot()

	require.Len(t, snapshot.Running, 1)
	assert.Equal(t, toolCallAt.Format(time.RFC3339), snapshot.Running[0].LastActivityAt)
	assert.Equal(t, "tool_call", snapshot.Running[0].LastActivityKind)
	assert.Equal(t, heartbeatAt.Format(time.RFC3339), snapshot.Running[0].LastHeartbeatAt)
}

func TestDiffStat_ParsesShortstat(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantAdded   int
		wantRemoved int
		wantFiles   int
	}{
		{name: "empty", output: "", wantAdded: 0, wantRemoved: 0, wantFiles: 0},
		{name: "insertions and deletions", output: " 2 files changed, 47 insertions(+), 3 deletions(-)\n", wantAdded: 47, wantRemoved: 3, wantFiles: 2},
		{name: "insertions only", output: " 1 file changed, 5 insertions(+)\n", wantAdded: 5, wantRemoved: 0, wantFiles: 1},
		{name: "deletions only", output: " 3 files changed, 8 deletions(-)\n", wantAdded: 0, wantRemoved: 8, wantFiles: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed, files := parseDiffShortstat(tt.output)
			assert.Equal(t, tt.wantAdded, added)
			assert.Equal(t, tt.wantRemoved, removed)
			assert.Equal(t, tt.wantFiles, files)
		})
	}
}

func TestDiffStat_TimeoutReturnsTimeoutStatus(t *testing.T) {
	if os.Getenv("CONTRABASS_DIFFSTAT_SLEEP") == "1" {
		time.Sleep(2 * time.Second)
		return
	}

	original := diffStatCommand
	diffStatCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestDiffStat_TimeoutReturnsTimeoutStatus")
		cmd.Env = append(os.Environ(), "CONTRABASS_DIFFSTAT_SLEEP=1")
		return cmd
	}
	t.Cleanup(func() {
		diffStatCommand = original
	})

	added, removed, files, status := diffStat(context.Background(), t.TempDir())

	assert.Zero(t, added)
	assert.Zero(t, removed)
	assert.Zero(t, files)
	assert.Equal(t, "timeout", status)
}

func TestReadIterationProgress(t *testing.T) {
	tests := []struct {
		name          string
		content       *string
		wantIteration int
		wantMax       int
	}{
		{name: "missing"},
		{name: "well formed", content: stringPtr(`{"iteration":3,"max_iterations":50}`), wantIteration: 3, wantMax: 50},
		{name: "corrupt", content: stringPtr(`{"iteration":`), wantIteration: 0, wantMax: 0},
		{name: "partial", content: stringPtr(`{"iteration":7}`), wantIteration: 7, wantMax: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			if tt.content != nil {
				stateDir := filepath.Join(workspace, ".omx", "state")
				require.NoError(t, os.MkdirAll(stateDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(stateDir, "run-state.json"), []byte(*tt.content), 0o644))
			}

			iteration, max := readIterationProgress(workspace)

			assert.Equal(t, tt.wantIteration, iteration)
			assert.Equal(t, tt.wantMax, max)
		})
	}
}

func stringPtr(value string) *string {
	return &value
}

func TestSnapshot_IncludesBackoffEntries(t *testing.T) {
	retryTime := time.Now().Add(5 * time.Minute)
	backoffEntry := types.BackoffEntry{
		IssueID: "issue-2",
		Attempt: 2,
		RetryAt: retryTime,
		Error:   "timeout",
	}

	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{backoffEntry},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{},
	}

	snapshot := o.Snapshot()

	require.Len(t, snapshot.Backoff, 1)
	assert.Equal(t, "issue-2", snapshot.Backoff[0].IssueID)
	assert.Equal(t, 2, snapshot.Backoff[0].Attempt)
	assert.Equal(t, retryTime, snapshot.Backoff[0].RetryAt)
	assert.Equal(t, "timeout", snapshot.Backoff[0].Error)
}

func TestSnapshot_IsIsolatedFromState(t *testing.T) {
	issue := types.Issue{
		ID:    "issue-1",
		Title: "Original Title",
	}

	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{},
		issueCache: map[string]types.Issue{"issue-1": issue},
		stats:      Stats{Running: 1},
	}

	snapshot := o.Snapshot()

	assert.Equal(t, "Original Title", snapshot.Issues["issue-1"].Title)
	assert.Equal(t, 1, snapshot.Stats.Running)

	o.mu.Lock()
	o.issueCache["issue-1"] = types.Issue{
		ID:    "issue-1",
		Title: "Modified Title",
	}
	o.stats.Running = 5
	o.mu.Unlock()

	assert.Equal(t, "Original Title", snapshot.Issues["issue-1"].Title)
	assert.Equal(t, 1, snapshot.Stats.Running)
}

func TestClassifyAgentStage_RuleTable(t *testing.T) {
	baseTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		lastActivityKind string
		diffAdded        int
		diffRemoved      int
		tokensPerMin     float64
		lastDiffChange   time.Time
		wantStage        string
		wantStep         int
	}{
		{
			name:             "Wrapping on turn/completed",
			lastActivityKind: "turn/completed",
			wantStage:        "Wrapping",
			wantStep:         5,
		},
		{
			name:             "Wrapping on turn/failed",
			lastActivityKind: "turn/failed",
			wantStage:        "Wrapping",
			wantStep:         5,
		},
		{
			name:             "Wrapping on turn/cancelled",
			lastActivityKind: "turn/cancelled",
			wantStage:        "Wrapping",
			wantStep:         5,
		},
		{
			name:             "Reviewing when diff plateaued >60s and tokens slow",
			lastActivityKind: "tool_call",
			diffAdded:        0,
			diffRemoved:      0,
			tokensPerMin:     40_000,
			lastDiffChange:   baseTime.Add(-90 * time.Second),
			wantStage:        "Reviewing",
			wantStep:         4,
		},
		{
			name:             "Testing when diff plateaued >30s",
			lastActivityKind: "tool_call",
			diffAdded:        0,
			diffRemoved:      0,
			tokensPerMin:     60_000,
			lastDiffChange:   baseTime.Add(-45 * time.Second),
			wantStage:        "Testing",
			wantStep:         3,
		},
		{
			name:             "Editing when diff growing",
			lastActivityKind: "tool_call",
			diffAdded:        10,
			diffRemoved:      2,
			tokensPerMin:     80_000,
			lastDiffChange:   baseTime.Add(-5 * time.Second),
			wantStage:        "Editing",
			wantStep:         2,
		},
		{
			name:             "Exploration when no diff and no plateau",
			lastActivityKind: "tool_call",
			diffAdded:        0,
			diffRemoved:      0,
			tokensPerMin:     5_000,
			lastDiffChange:   baseTime.Add(-10 * time.Second),
			wantStage:        "Exploration",
			wantStep:         1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &agentStageState{
				PrevStep:       0,
				LastDiffChange: tt.lastDiffChange,
			}
			stage, step := classifyAgentStage(state, tt.lastActivityKind, tt.diffAdded, tt.diffRemoved, tt.tokensPerMin, baseTime)
			assert.Equal(t, tt.wantStage, stage)
			assert.Equal(t, tt.wantStep, step)
		})
	}
}

func TestClassifyAgentStage_MonotonicClamp(t *testing.T) {
	baseTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	state := &agentStageState{
		PrevStep:       0,
		LastDiffChange: baseTime.Add(-90 * time.Second),
	}

	// First call: diff plateaued >60s, slow tokens → Reviewing (4).
	stage1, step1 := classifyAgentStage(state, "tool_call", 0, 0, 40_000, baseTime)
	assert.Equal(t, "Reviewing", stage1)
	assert.Equal(t, 4, step1)

	// Second call: diff growing again would normally yield Editing (2), but clamp holds at 4.
	stage2, step2 := classifyAgentStage(state, "tool_call", 20, 5, 80_000, baseTime.Add(time.Second))
	assert.Equal(t, "Reviewing", stage2)
	assert.Equal(t, 4, step2)
}

func TestEstimateCompletionAt_LowEarly(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-90 * time.Second)

	etaAt, conf := estimateCompletionAt(startedAt, now, 5, 1000, 2000, 2, 0)

	assert.Equal(t, "", etaAt)
	assert.Equal(t, "low", conf)
}

func TestEstimateCompletionAt_LowQuiet(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-10 * time.Minute)

	// tokensPerMin = (100+100)/10 = 20 — well below 1000.
	etaAt, conf := estimateCompletionAt(startedAt, now, 1, 100, 100, 2, 0)

	assert.Equal(t, "", etaAt)
	assert.Equal(t, "low", conf)
}

func TestEstimateCompletionAt_Medium(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-6 * time.Minute)

	// filesPerMin = 5/6 ≈ 0.83 (>0.05), tokensPerMin = (10000+5000)/6 = 2500 (>1000).
	// elapsed=6>5 but not >8 with stage=2 → medium.
	etaAt, conf := estimateCompletionAt(startedAt, now, 5, 10_000, 5_000, 2, 0)

	assert.Equal(t, "medium", conf)
	require.NotEmpty(t, etaAt)

	parsed, err := time.Parse(time.RFC3339, etaAt)
	require.NoError(t, err)
	assert.True(t, parsed.After(now), "ETA should be in the future")
}

func TestEstimateCompletionAt_High(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-12 * time.Minute)

	// elapsed=12>8, stageStep=4≥3 → high.
	etaAt, conf := estimateCompletionAt(startedAt, now, 8, 50_000, 20_000, 4, 0)

	assert.Equal(t, "high", conf)
	require.NotEmpty(t, etaAt)

	parsed, err := time.Parse(time.RFC3339, etaAt)
	require.NoError(t, err)
	assert.True(t, parsed.After(startedAt))
}

func TestSnapshot_StagePropagatesAcrossTicks(t *testing.T) {
	now := time.Now()
	issue := types.Issue{ID: "stage-issue"}
	attempt := types.RunAttempt{
		IssueID:   "stage-issue",
		Attempt:   1,
		StartTime: now.Add(-10 * time.Minute),
		Phase:     types.StreamingTurn,
		TokensIn:  20_000,
		TokensOut: 10_000,
	}
	entry := &runEntry{
		issue:     issue,
		attempt:   attempt,
		workspace: t.TempDir(),
		stageState: agentStageState{
			PrevStep:       3,
			LastDiffChange: now.Add(-120 * time.Second),
		},
	}

	o := &Orchestrator{
		running:    map[string]*runEntry{"stage-issue": entry},
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{},
	}

	snap1 := o.Snapshot()
	require.Len(t, snap1.Running, 1)
	step1 := snap1.Running[0].AgentStageStep

	snap2 := o.Snapshot()
	require.Len(t, snap2.Running, 1)
	step2 := snap2.Running[0].AgentStageStep

	assert.GreaterOrEqual(t, step2, step1, "stage step must never decrease")
	assert.GreaterOrEqual(t, step1, 1)
}

func TestSnapshot_ReleasesAgentStageState(t *testing.T) {
	now := time.Now()
	issue := types.Issue{ID: "release-issue"}
	attempt := types.RunAttempt{
		IssueID:   "release-issue",
		Attempt:   1,
		StartTime: now.Add(-5 * time.Minute),
		Phase:     types.StreamingTurn,
		TokensIn:  5_000,
		TokensOut: 2_000,
	}
	entry := &runEntry{
		issue:     issue,
		attempt:   attempt,
		workspace: t.TempDir(),
	}

	o := &Orchestrator{
		running:    map[string]*runEntry{"release-issue": entry},
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{Running: 1},
	}

	// First tick — entry is live.
	snap1 := o.Snapshot()
	require.Len(t, snap1.Running, 1)

	// Simulate run release.
	o.mu.Lock()
	delete(o.running, "release-issue")
	o.stats.Running = 0
	o.mu.Unlock()

	// Second tick — running map should be empty.
	snap2 := o.Snapshot()
	assert.Len(t, snap2.Running, 0)
	assert.Equal(t, 0, len(o.running))
}

func TestClassifyAgentStage_ZeroLastDiffChangeStartsAtExploration(t *testing.T) {
	baseTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// Zero value, exactly as on a freshly-created runEntry: without the
	// first-tick anchor, elapsed would be measured from the zero time and the
	// run would classify as Reviewing (4) with the clamp locking it there.
	state := &agentStageState{}

	stage, step := classifyAgentStage(state, "tool_call", 0, 0, 0, baseTime)
	assert.Equal(t, "Exploration", stage)
	assert.Equal(t, 1, step)
	assert.Equal(t, baseTime, state.LastDiffChange, "plateau clock must anchor at the first tick")

	// The clamp must not have locked the run: a growing diff on the next
	// tick advances to Editing.
	stage2, step2 := classifyAgentStage(state, "tool_call", 10, 2, 80_000, baseTime.Add(5*time.Second))
	assert.Equal(t, "Editing", stage2)
	assert.Equal(t, 2, step2)
}
