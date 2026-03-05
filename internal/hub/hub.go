package hub

import (
	"context"
	"sync"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
)

const defaultSubscriberBufferSize = 256

type Hub struct {
	mu          sync.RWMutex
	subscribers map[int]chan orchestrator.OrchestratorEvent
	nextID      int
	source      <-chan orchestrator.OrchestratorEvent
}

func NewHub(source <-chan orchestrator.OrchestratorEvent) *Hub {
	return &Hub{
		subscribers: make(map[int]chan orchestrator.OrchestratorEvent),
		source:      source,
	}
}

func (h *Hub) Subscribe() (int, <-chan orchestrator.OrchestratorEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++

	ch := make(chan orchestrator.OrchestratorEvent, defaultSubscriberBufferSize)
	h.subscribers[id] = ch

	return id, ch
}

func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, ok := h.subscribers[id]
	if !ok {
		return
	}

	delete(h.subscribers, id)
	close(ch)
}

func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.subscribers)
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.closeAllSubscribersLocked()
			h.mu.Unlock()
			return
		case event, ok := <-h.source:
			if !ok {
				h.mu.Lock()
				h.closeAllSubscribersLocked()
				h.mu.Unlock()
				return
			}

			h.mu.RLock()
			for _, sub := range h.subscribers {
				select {
				case sub <- event:
				default:
					// drop for slow subscriber
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) closeAllSubscribersLocked() {
	for id, sub := range h.subscribers {
		close(sub)
		delete(h.subscribers, id)
	}
}
