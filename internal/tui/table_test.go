package tui

import (
	"testing"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestTableEmpty(t *testing.T) {
	tbl := NewTable()
	out := tbl.View()
	assert.Contains(t, out, "No agents running")
}

func TestTableWithRows(t *testing.T) {
	rows := []AgentRow{
		{IssueID: "ISSUE-123", Stage: "StreamingTurn", PID: 4567, Age: "2m", Turn: 3, TokensIn: 1500, TokensOut: 800, SessionID: "sess-abc", LastEvent: "tool_use", Phase: types.StreamingTurn},
		{IssueID: "ISSUE-456", Stage: "BuildingPrompt", PID: 7890, Age: "30s", Turn: 1, TokensIn: 500, TokensOut: 100, SessionID: "sess-def", LastEvent: "init", Phase: types.BuildingPrompt},
	}
	tbl := NewTable().Update(rows, "●")
	out := tbl.View()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STAGE")
	assert.Contains(t, out, "ISSUE-123")
	assert.Contains(t, out, "ISSUE-456")
}

func TestStatusIndicator(t *testing.T) {
	activePhases := map[types.RunPhase]bool{
		types.StreamingTurn:         true,
		types.Finishing:             true,
		types.InitializingSession:   true,
		types.LaunchingAgentProcess: true,
		types.PreparingWorkspace:    true,
		types.BuildingPrompt:        true,
	}

	tests := []struct {
		name  string
		phase types.RunPhase
	}{
		{"streaming", types.StreamingTurn},
		{"finishing", types.Finishing},
		{"initializing", types.InitializingSession},
		{"launching", types.LaunchingAgentProcess},
		{"preparing", types.PreparingWorkspace},
		{"building", types.BuildingPrompt},
		{"failed", types.Failed},
		{"timedout", types.TimedOut},
		{"stalled", types.Stalled},
		{"canceled", types.CanceledByReconciliation},
		{"succeeded", types.Succeeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := statusIndicator(tt.phase, "⠋")
			assert.NotEmpty(t, result)
			stripped := stripANSI(result)
			if activePhases[tt.phase] {
				assert.NotContains(t, stripped, "●", "active phase should use spinner frame")
				assert.Contains(t, stripped, "⠋", "active phase should contain spinner frame")
			} else {
				assert.Contains(t, stripped, "●", "terminal phase should use static dot")
			}
		})
	}
}

func TestTruncateSessionID(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		maxLen int
		want   string
	}{
		{"short", "abc", 10, "abc"},
		{"exact", "abcdefghij", 10, "abcdefghij"},
		{"long", "abcdefghijklmnop", 10, "abcdefg..."},
		{"empty", "", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, truncateSessionID(tt.id, tt.maxLen))
		})
	}
}

func TestFormatTokensShort(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"small", 500, "500"},
		{"kilo", 1234, "1.2k"},
		{"mega", 1500000, "1.5M"},
		{"zero", 0, "0"},
		{"boundary_k", 1000, "1.0k"},
		{"boundary_m", 1000000, "1.0M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatTokensShort(tt.n))
		})
	}
}

func TestTableSetWidth(t *testing.T) {
	tbl := NewTable().SetWidth(120)
	rows := []AgentRow{{IssueID: "X-1", Phase: types.Succeeded}}
	tbl = tbl.Update(rows, "●")
	out := tbl.View()
	assert.Contains(t, out, "X-1")
}
