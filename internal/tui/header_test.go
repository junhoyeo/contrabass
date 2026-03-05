package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaderViewContainsTitle(t *testing.T) {
	h := NewHeader()
	h = h.Update(HeaderData{RunningAgents: 3, MaxAgents: 10, RuntimeSeconds: 154})
	out := h.View()
	assert.Contains(t, out, "CONTRABASS STATUS")
	assert.Contains(t, out, "____")
	assert.Contains(t, out, "3/10")
	assert.Contains(t, out, "collecting...")
}

func TestFormatRuntime(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    string
	}{
		{"zero", 0, "0s"},
		{"seconds only", 45, "45s"},
		{"one minute", 60, "1m 0s"},
		{"mixed", 154, "2m 34s"},
		{"negative clamped", -5, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatRuntime(tt.seconds))
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0"},
		{"small", 999, "999"},
		{"thousands", 1234, "1,234"},
		{"millions", 1234567, "1,234,567"},
		{"negative", -1234, "-1,234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatTokens(tt.n))
		})
	}
}

func TestFormatThroughput(t *testing.T) {
	tests := []struct {
		name string
		tps  float64
		want string
	}{
		{"zero", 0.0, "collecting..."},
		{"small", 12.3, "12.3 tok/s"},
		{"large", 1234.5, "1,234.5 tok/s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := int64(1)
			if tt.name == "zero" {
				tokens = 0
			}
			assert.Equal(t, tt.want, formatThroughput(tt.tps, tokens))
		})
	}
}

func TestHeaderZeroData(t *testing.T) {
	h := NewHeader()
	assert.NotPanics(t, func() {
		out := h.View()
		assert.Contains(t, out, "CONTRABASS STATUS")
		assert.Contains(t, out, "0/0")
		assert.Contains(t, out, "collecting...")
	})
}

func TestHeaderSetWidth(t *testing.T) {
	h := NewHeader().SetWidth(80)
	h = h.Update(HeaderData{RunningAgents: 1, MaxAgents: 5})
	out := h.View()
	assert.Contains(t, out, "CONTRABASS STATUS")
}
