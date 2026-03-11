package team

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventConsumer_SubscribeReceivesEvents(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "subscriber receives logged events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 10*time.Millisecond)

			sub := consumer.Subscribe()
			require.Equal(t, 1, consumer.SubscriberCount())

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			require.NoError(t, logger.Log("team-a", LoggedEvent{Type: "task_claimed", TaskID: "task-1"}))

			event := receiveEvent(t, sub, time.Second)
			assert.Equal(t, "task_claimed", event.Type)
			assert.Equal(t, "task-1", event.TaskID)

			cancel()
			require.NoError(t, <-runDone)
		})
	}
}

func TestEventConsumer_MultipleSubscribersReceiveSameEvents(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "all subscribers receive same event"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 10*time.Millisecond)

			subA := consumer.Subscribe()
			subB := consumer.Subscribe()
			require.Equal(t, 2, consumer.SubscriberCount())

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			require.NoError(t, logger.Log("team-a", LoggedEvent{ID: "evt-1", Type: "task_completed", TaskID: "task-7"}))

			eventA := receiveEvent(t, subA, time.Second)
			eventB := receiveEvent(t, subB, time.Second)
			assert.Equal(t, "evt-1", eventA.ID)
			assert.Equal(t, "evt-1", eventB.ID)
			assert.Equal(t, eventA.TaskID, eventB.TaskID)

			cancel()
			require.NoError(t, <-runDone)
		})
	}
}

func TestEventConsumer_UnsubscribeRemovesSubscriber(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "unsubscribed channel stops receiving"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 10*time.Millisecond)

			removed := consumer.Subscribe()
			active := consumer.Subscribe()
			consumer.Unsubscribe(removed)
			require.Equal(t, 1, consumer.SubscriberCount())

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			require.NoError(t, logger.Log("team-a", LoggedEvent{Type: "worker_started", WorkerID: "worker-1"}))

			event := receiveEvent(t, active, time.Second)
			assert.Equal(t, "worker_started", event.Type)
			assert.Equal(t, "worker-1", event.WorkerID)

			assertNoEvent(t, removed, 200*time.Millisecond)

			cancel()
			require.NoError(t, <-runDone)
		})
	}
}

func TestEventConsumer_RunPollsAndDeliversNewEvents(t *testing.T) {
	tests := []struct {
		name  string
		types []string
	}{
		{name: "run polls and streams new logged events", types: []string{"task_claimed", "task_completed"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 10*time.Millisecond)
			sub := consumer.Subscribe()

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			for i, eventType := range tt.types {
				require.NoError(t, logger.Log("team-a", LoggedEvent{
					Type:   eventType,
					TaskID: fmt.Sprintf("task-%d", i+1),
				}))
			}

			for _, eventType := range tt.types {
				event := receiveEvent(t, sub, time.Second)
				assert.Equal(t, eventType, event.Type)
			}

			cancel()
			require.NoError(t, <-runDone)
		})
	}
}

func TestEventConsumer_RunStopsOnContextCancel(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "run exits and closes subscriber channels"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 20*time.Millisecond)
			sub := consumer.Subscribe()

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			cancel()
			require.NoError(t, <-runDone)

			_, ok := <-sub
			assert.False(t, ok)
			assert.Equal(t, 0, consumer.SubscriberCount())
		})
	}
}

func TestEventConsumer_NonBlockingSend(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "slow subscriber does not block others"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewEventLogger(NewPaths(t.TempDir()))
			consumer := NewEventConsumer(logger, "team-a", 5*time.Millisecond)

			_ = consumer.Subscribe()
			fast := consumer.Subscribe()

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- consumer.Run(ctx) }()

			var fastCount int64
			drainDone := make(chan struct{})
			go func() {
				defer close(drainDone)
				for range fast {
					atomic.AddInt64(&fastCount, 1)
				}
			}()

			for i := range 200 {
				require.NoError(t, logger.Log("team-a", LoggedEvent{Type: "task_progress", TaskID: fmt.Sprintf("task-%d", i)}))
			}

			assert.Eventually(t, func() bool {
				return atomic.LoadInt64(&fastCount) >= 120
			}, 2*time.Second, 20*time.Millisecond)

			cancel()
			require.NoError(t, <-runDone)
			<-drainDone
		})
	}
}

func receiveEvent(t *testing.T, ch <-chan LoggedEvent, timeout time.Duration) LoggedEvent {
	t.Helper()

	select {
	case event, ok := <-ch:
		require.True(t, ok, "channel closed before receiving event")
		return event
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for event after %s", timeout)
		return LoggedEvent{}
	}
}

func assertNoEvent(t *testing.T, ch <-chan LoggedEvent, duration time.Duration) {
	t.Helper()

	select {
	case event, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event received: %+v", event)
		}
	case <-time.After(duration):
	}
}
