package hub

import (
	"context"
	"sync"
)

const defaultSubscriberBufferSize = 256

type Hub[T any] struct {
	mu          sync.RWMutex
	subscribers map[int]chan T
	nextID      int
	source      <-chan T
}

func NewHub[T any](source <-chan T) *Hub[T] {
	return &Hub[T]{
		subscribers: make(map[int]chan T),
		source:      source,
	}
}

func (h *Hub[T]) Subscribe() (int, <-chan T) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++

	ch := make(chan T, defaultSubscriberBufferSize)
	h.subscribers[id] = ch

	return id, ch
}

func (h *Hub[T]) Unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, ok := h.subscribers[id]
	if !ok {
		return
	}

	delete(h.subscribers, id)
	close(ch)
}

func (h *Hub[T]) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.subscribers)
}

func (h *Hub[T]) Run(ctx context.Context) {
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

func (h *Hub[T]) closeAllSubscribersLocked() {
	for id, sub := range h.subscribers {
		close(sub)
		delete(h.subscribers, id)
	}
}
