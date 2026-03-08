package hub

import (
	"context"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "single_subscriber_receives_events",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent, 1)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)

				done := make(chan struct{})
				go func() {
					h.Run(ctx)
					close(done)
				}()

				_, sub := h.Subscribe()
				event := testEvent("issue-1")

				source <- event

				received := mustReceiveEvent(t, sub)
				assert.Equal(t, event, received)

				cancel()
				waitDone(t, done)
			},
		},
		{
			name: "multiple_subscribers_receive_same_event",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent, 1)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)

				done := make(chan struct{})
				go func() {
					h.Run(ctx)
					close(done)
				}()

				_, sub1 := h.Subscribe()
				_, sub2 := h.Subscribe()
				_, sub3 := h.Subscribe()

				event := testEvent("issue-2")
				source <- event

				assert.Equal(t, event, mustReceiveEvent(t, sub1))
				assert.Equal(t, event, mustReceiveEvent(t, sub2))
				assert.Equal(t, event, mustReceiveEvent(t, sub3))

				cancel()
				waitDone(t, done)
			},
		},
		{
			name: "slow_subscriber_does_not_block_other_subscribers",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent, defaultSubscriberBufferSize+16)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)

				done := make(chan struct{})
				go func() {
					h.Run(ctx)
					close(done)
				}()

				_, slowSub := h.Subscribe()
				_, fastSub := h.Subscribe()

				for i := 0; i < defaultSubscriberBufferSize; i++ {
					source <- testEvent("fill")
				}

				require.Eventually(t, func() bool {
					return len(slowSub) == defaultSubscriberBufferSize
				}, 5*time.Second, 10*time.Millisecond)

				for len(fastSub) > 0 {
					<-fastSub
				}

				source <- testEvent("last")

				select {
				case event := <-fastSub:
					assert.Equal(t, "last", event.IssueID)
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for fast subscriber to receive event")
				}

				assert.Equal(t, defaultSubscriberBufferSize, len(slowSub))

				cancel()
				waitDone(t, done)
			},
		},
		{
			name: "unsubscribe_removes_subscriber_and_closes_channel",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				id, sub := h.Subscribe()
				require.Equal(t, 1, h.SubscriberCount())

				h.Unsubscribe(id)

				require.Equal(t, 0, h.SubscriberCount())
				_, ok := <-sub
				assert.False(t, ok)
			},
		},
		{
			name: "context_cancellation_closes_all_subscribers",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				_, sub1 := h.Subscribe()
				_, sub2 := h.Subscribe()

				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					h.Run(ctx)
					close(done)
				}()

				cancel()
				waitDone(t, done)

				_, ok1 := <-sub1
				_, ok2 := <-sub2
				assert.False(t, ok1)
				assert.False(t, ok2)
				assert.Equal(t, 0, h.SubscriberCount())
			},
		},
		{
			name: "subscriber_count_tracks_add_and_remove",
			run: func(t *testing.T) {
				source := make(chan orchestrator.OrchestratorEvent)
				h := NewHub[orchestrator.OrchestratorEvent](source)

				id1, _ := h.Subscribe()
				id2, _ := h.Subscribe()
				id3, _ := h.Subscribe()

				assert.Equal(t, 3, h.SubscriberCount())

				h.Unsubscribe(id2)
				assert.Equal(t, 2, h.SubscriberCount())

				h.Unsubscribe(id1)
				h.Unsubscribe(id3)
				assert.Equal(t, 0, h.SubscriberCount())
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func mustReceiveEvent(t *testing.T, ch <-chan orchestrator.OrchestratorEvent) orchestrator.OrchestratorEvent {
	t.Helper()

	select {
	case event, ok := <-ch:
		require.True(t, ok)
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for event")
		return orchestrator.OrchestratorEvent{}
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for hub run loop to exit")
	}
}

func testEvent(issueID string) orchestrator.OrchestratorEvent {
	return orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   issueID,
		Data:      nil,
		Timestamp: time.Now(),
	}
}
