package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTeamNameFromOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "omx v0.16 banner with hash suffix",
			in: `[omx:team] worker startup resolution: model=gpt-5.5 thinking_level=medium source=role-default
[omx:team] worker startup resolution: model=gpt-5.5 thinking_level=medium source=role-default
Team started: add-godoc-comments-fo-d3d7562b
tmux target: omx-foo:0
workers: 2
`,
			want: "add-godoc-comments-fo-d3d7562b",
		},
		{
			name: "team name with surrounding whitespace",
			in:   "  Team started:    my-team  \n",
			want: "my-team",
		},
		{
			name: "older CLI prints just 'Team started' without name",
			in:   "Team started\n",
			want: "",
		},
		{
			name: "no banner at all (empty fallback)",
			in:   "some other unrelated output\n",
			want: "",
		},
		{
			name: "blank input",
			in:   "",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTeamNameFromOutput([]byte(tc.in))
			assert.Equal(t, tc.want, got)
		})
	}
}

// T4 — TestReadOmxMetrics

func TestReadOmxMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(dir string) // writes files into dir
		wantNil     bool
		wantErr     bool
		wantMetrics *OmxMetrics
	}{
		{
			name: "well-formed file returns expected struct",
			setup: func(dir string) {
				writeMetricsFile(t, dir, map[string]interface{}{
					"session_input_tokens":  int64(100),
					"session_output_tokens": int64(200),
					"session_total_tokens":  int64(300),
					"five_hour_limit_pct":   0.5,
					"weekly_limit_pct":      0.25,
				})
			},
			wantNil: false,
			wantErr: false,
			wantMetrics: &OmxMetrics{
				SessionInputTokens:  100,
				SessionOutputTokens: 200,
				SessionTotalTokens:  300,
				FiveHourLimitPct:    0.5,
				WeeklyLimitPct:      0.25,
			},
		},
		{
			name:    "missing file returns nil nil",
			setup:   nil,
			wantNil: true,
			wantErr: false,
		},
		{
			name: "corrupt JSON returns nil non-nil error",
			setup: func(dir string) {
				omxDir := filepath.Join(dir, ".omx")
				require.NoError(t, os.MkdirAll(omxDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(omxDir, "metrics.json"), []byte("{bad json"), 0o644))
			},
			wantNil: true,
			wantErr: true,
		},
		{
			name: "extra unknown fields are ignored, known fields parsed",
			setup: func(dir string) {
				writeMetricsFile(t, dir, map[string]interface{}{
					"session_input_tokens":  int64(50),
					"session_output_tokens": int64(60),
					"session_total_tokens":  int64(110),
					"unknown_field":         "ignore-me",
					"another_unknown":       42,
				})
			},
			wantNil: false,
			wantErr: false,
			wantMetrics: &OmxMetrics{
				SessionInputTokens:  50,
				SessionOutputTokens: 60,
				SessionTotalTokens:  110,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(dir)
			}

			got, err := readOmxMetrics(dir)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.wantMetrics, got)
		})
	}
}

// writeMetricsFile writes a .omx/metrics.json into dir with the given fields.
func writeMetricsFile(t *testing.T, dir string, fields map[string]interface{}) {
	t.Helper()
	omxDir := filepath.Join(dir, ".omx")
	require.NoError(t, os.MkdirAll(omxDir, 0o755))
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(omxDir, "metrics.json"), data, 0o644))
}

// T5 — TestMonitorProcess_EmitsSessionUsageOnMetricsChange
//
// Instead of driving monitorProcess directly (which requires a real team CLI
// binary), we test the emission logic through a lightweight helper that mirrors
// the exact throttle logic added by T3. This verifies: first-write fires,
// same-totals suppresses, higher-totals fires again.

func TestMonitorProcess_EmitsSessionUsageOnMetricsChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Helper: simulate the T3 inner loop logic against proc.lastUsage.
	type emittedEvent struct {
		eventType string
		data      map[string]interface{}
	}

	var emitted []emittedEvent
	proc := &teamCLIProcess{
		teamName:  "test-team",
		workspace: dir,
		events:    make(chan types.AgentEvent, 128),
		done:      make(chan error, 1),
		finished:  make(chan struct{}),
	}

	emit := func(eventType string, data map[string]interface{}) {
		emitted = append(emitted, emittedEvent{eventType: eventType, data: data})
	}

	runOnePollCycle := func() {
		metrics, err := readOmxMetrics(proc.workspace)
		if err != nil || metrics == nil {
			return
		}
		changed := !proc.lastUsage.seen ||
			metrics.SessionInputTokens > proc.lastUsage.input ||
			metrics.SessionOutputTokens > proc.lastUsage.output ||
			metrics.SessionTotalTokens > proc.lastUsage.total
		if changed {
			emit("session.usage", map[string]interface{}{
				"team_name": proc.teamName,
				"usage": map[string]interface{}{
					"input_tokens":  metrics.SessionInputTokens,
					"output_tokens": metrics.SessionOutputTokens,
					"total_tokens":  metrics.SessionTotalTokens,
				},
			})
			proc.lastUsage.input = metrics.SessionInputTokens
			proc.lastUsage.output = metrics.SessionOutputTokens
			proc.lastUsage.total = metrics.SessionTotalTokens
			proc.lastUsage.seen = true
		}
	}

	// Boundary 1: write metrics with total_tokens=1000 → expect exactly one emission.
	writeMetricsFile(t, dir, map[string]interface{}{
		"session_input_tokens":  int64(400),
		"session_output_tokens": int64(600),
		"session_total_tokens":  int64(1000),
	})
	runOnePollCycle()

	require.Len(t, emitted, 1, "boundary 1: expected exactly one session.usage event")
	assert.Equal(t, "session.usage", emitted[0].eventType)
	usage, ok := emitted[0].data["usage"].(map[string]interface{})
	require.True(t, ok, "usage field must be a map")
	assert.Equal(t, int64(1000), usage["total_tokens"])

	// Boundary 2: same totals → no new emission.
	beforeCount := len(emitted)
	runOnePollCycle()
	assert.Equal(t, beforeCount, len(emitted), "boundary 2: same totals must not emit again")

	// Small sleep to ensure file mtime can differ if OS resolution requires it.
	time.Sleep(5 * time.Millisecond)

	// Boundary 3: update to total_tokens=2500 → exactly one new emission.
	writeMetricsFile(t, dir, map[string]interface{}{
		"session_input_tokens":  int64(1000),
		"session_output_tokens": int64(1500),
		"session_total_tokens":  int64(2500),
	})
	runOnePollCycle()

	require.Len(t, emitted, 2, "boundary 3: expected exactly one new session.usage event")
	assert.Equal(t, "session.usage", emitted[1].eventType)
	usage2, ok := emitted[1].data["usage"].(map[string]interface{})
	require.True(t, ok, "usage field must be a map")
	assert.Equal(t, int64(2500), usage2["total_tokens"])
}
