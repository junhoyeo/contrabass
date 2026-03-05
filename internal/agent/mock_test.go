package agent

import (
	"context"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockRunner_HandshakeSequence(t *testing.T) {
	t.Parallel()

	handshake := []types.AgentEvent{
		{Type: "initialize"},
		{Type: "initialized"},
		{Type: "thread/start"},
		{Type: "turn/start"},
	}
	regular := []types.AgentEvent{
		{Type: "turn/completed"},
	}

	runner := &MockRunner{
		HandshakeEvents: handshake,
		Events:          regular,
	}

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MOCK-1"}, "/tmp", "test")
	require.NoError(t, err)

	var collected []string
	for ev := range proc.Events {
		collected = append(collected, ev.Type)
	}

	expected := []string{"initialize", "initialized", "thread/start", "turn/start", "turn/completed"}
	assert.Equal(t, expected, collected)

	// Handshake events should precede regular events
	for i, typ := range collected[:4] {
		assert.Equal(t, handshake[i].Type, typ)
	}
	assert.Equal(t, "turn/completed", collected[4])

	// Timestamps should be filled in
	select {
	case doneErr := <-proc.Done:
		assert.NoError(t, doneErr)
	case <-time.After(2 * time.Second):
		t.Fatal("done channel not signaled")
	}
}

func TestMockRunner_HandshakeSequence_NoHandshake(t *testing.T) {
	t.Parallel()

	runner := &MockRunner{
		Events: []types.AgentEvent{
			{Type: "turn/completed"},
		},
	}

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MOCK-2"}, "/tmp", "test")
	require.NoError(t, err)

	var collected []string
	for ev := range proc.Events {
		collected = append(collected, ev.Type)
	}

	assert.Equal(t, []string{"turn/completed"}, collected)
}

func TestMockRunner_StopDelaySimulation(t *testing.T) {
	t.Parallel()

	stopDelay := 100 * time.Millisecond
	runner := &MockRunner{
		Events: []types.AgentEvent{
			{Type: "turn/started"},
		},
		Delay:     500 * time.Millisecond, // slow emission so stop fires during delay
		StopDelay: stopDelay,
	}

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MOCK-3"}, "/tmp", "test")
	require.NoError(t, err)

	// Drain first event so goroutine is in the Delay sleep
	select {
	case <-proc.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}

	start := time.Now()
	err = runner.Stop(proc)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, stopDelay, "Stop should take at least StopDelay")

	select {
	case <-proc.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("done channel not signaled after stop")
	}
}

func TestMockRunner_StopDelayZeroIsImmediate(t *testing.T) {
	t.Parallel()

	runner := &MockRunner{
		Events: []types.AgentEvent{
			{Type: "turn/started"},
		},
		Delay: 500 * time.Millisecond,
		// StopDelay is zero (default) — stop should be immediate
	}

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MOCK-4"}, "/tmp", "test")
	require.NoError(t, err)

	select {
	case <-proc.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}

	start := time.Now()
	err = runner.Stop(proc)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 50*time.Millisecond, "Stop with zero StopDelay should be near-instant")
}
