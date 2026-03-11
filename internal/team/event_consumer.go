package team

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

const defaultEventConsumerPollInterval = 500 * time.Millisecond

type EventConsumer struct {
	logger       *EventLogger
	teamName     string
	pollInterval time.Duration
	subscribers  []chan<- LoggedEvent
	mu           sync.Mutex
}

func NewEventConsumer(logger *EventLogger, teamName string, pollInterval time.Duration) *EventConsumer {
	if pollInterval <= 0 {
		pollInterval = defaultEventConsumerPollInterval
	}

	return &EventConsumer{
		logger:       logger,
		teamName:     teamName,
		pollInterval: pollInterval,
		subscribers:  make([]chan<- LoggedEvent, 0),
	}
}

func (c *EventConsumer) Subscribe() <-chan LoggedEvent {
	ch := make(chan LoggedEvent, 64)

	c.mu.Lock()
	c.subscribers = append(c.subscribers, ch)
	c.mu.Unlock()

	return ch
}

func (c *EventConsumer) Unsubscribe(ch <-chan LoggedEvent) {
	target := reflect.ValueOf(ch).Pointer()

	c.mu.Lock()
	defer c.mu.Unlock()

	filtered := make([]chan<- LoggedEvent, 0, len(c.subscribers))
	for _, sub := range c.subscribers {
		if reflect.ValueOf(sub).Pointer() == target {
			continue
		}
		filtered = append(filtered, sub)
	}

	c.subscribers = filtered
}

func (c *EventConsumer) SubscriberCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subscribers)
}

func (c *EventConsumer) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	defer c.closeSubscribers()

	var cursor int64

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			events, nextCursor, err := c.logger.ReadSince(c.teamName, cursor, nil)
			if err != nil {
				slog.Default().Warn("event consumer read failed", "team", c.teamName, "error", err)
				continue
			}

			cursor = nextCursor
			for _, event := range events {
				c.broadcast(event)
			}
		}
	}
}

func (c *EventConsumer) broadcast(event LoggedEvent) {
	c.mu.Lock()
	subscribers := append([]chan<- LoggedEvent(nil), c.subscribers...)
	c.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (c *EventConsumer) closeSubscribers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, ch := range c.subscribers {
		close(ch)
	}

	c.subscribers = nil
}
