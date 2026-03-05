package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

type MockRunner struct {
	Events          []types.AgentEvent
	HandshakeEvents []types.AgentEvent // optional: emitted before Events
	StartErr        error
	DoneErr         error
	Delay           time.Duration
	StopDelay       time.Duration // optional: delay before Stop completes
	pidSeq int64
	mu     sync.Mutex
	stops  map[int]chan struct{}
}

func (m *MockRunner) Start(ctx context.Context, _ types.Issue, _ string, _ string) (*AgentProcess, error) {
	if m.StartErr != nil {
		return nil, m.StartErr
	}

	pid := int(atomic.AddInt64(&m.pidSeq, 1))
	events := make(chan types.AgentEvent, len(m.HandshakeEvents)+len(m.Events))
	done := make(chan error, 1)
	stop := make(chan struct{})

	m.mu.Lock()
	if m.stops == nil {
		m.stops = make(map[int]chan struct{})
	}
	m.stops[pid] = stop
	m.mu.Unlock()

	go func() {
		defer close(events)
		defer close(done)

		for _, event := range m.HandshakeEvents {
			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now()
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-stop:
				done <- nil
				return
			case events <- event:
			}
		}
		for _, event := range m.Events {
			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now()
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-stop:
				done <- nil
				return
			case events <- event:
			}

			if m.Delay > 0 {
				timer := time.NewTimer(m.Delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					done <- ctx.Err()
					return
				case <-stop:
					timer.Stop()
					done <- nil
					return
				case <-timer.C:
				}
			}
		}

		done <- m.DoneErr
	}()

	return &AgentProcess{
		PID:       pid,
		SessionID: "mock-session",
		Events:    events,
		Done:      done,
	}, nil
}

func (m *MockRunner) Stop(proc *AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	m.mu.Lock()
	stop, ok := m.stops[proc.PID]
	if ok {
		delete(m.stops, proc.PID)
	}
	m.mu.Unlock()

	if ok {
		if m.StopDelay > 0 {
			time.Sleep(m.StopDelay)
		}
		close(stop)
	}

	return nil
}
