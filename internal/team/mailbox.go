package team

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// Mailbox manages per-worker message queuing for team coordination.
type Mailbox struct {
	store *Store
	paths *Paths
}

// NewMailbox creates a Mailbox backed by the given Store and Paths.
func NewMailbox(store *Store, paths *Paths) *Mailbox {
	return &Mailbox{store: store, paths: paths}
}

// generateMessageID returns a random hex ID for message identification.
func generateMessageID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate message id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Send enqueues a message from one worker to another.
func (m *Mailbox) Send(teamName, from, to, content string) error {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	id, err := generateMessageID()
	if err != nil {
		return err
	}

	msg := &types.MailboxMessage{
		ID:        id,
		From:      from,
		To:        to,
		Content:   content,
		Timestamp: time.Now(),
		Status:    types.MessagePending,
	}

	dir := m.paths.WorkerMailboxDir(teamName, to)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create mailbox dir: %w", err)
	}

	path := filepath.Join(dir, id+".json")
	return m.store.WriteJSON(path, msg)
}

// Broadcast sends a message from a sender to all specified workers.
func (m *Mailbox) Broadcast(teamName, from string, recipients []string, content string) error {
	for _, to := range recipients {
		if to == from {
			continue
		}
		if err := m.Send(teamName, from, to, content); err != nil {
			return fmt.Errorf("broadcast to %s: %w", to, err)
		}
	}
	return nil
}

// ListPending returns all pending messages for a worker.
func (m *Mailbox) ListPending(teamName, workerID string) ([]types.MailboxMessage, error) {
	dir := m.paths.WorkerMailboxDir(teamName, workerID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.MailboxMessage{}, nil
		}
		return nil, fmt.Errorf("read mailbox dir: %w", err)
	}

	var messages []types.MailboxMessage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var msg types.MailboxMessage
		path := filepath.Join(dir, entry.Name())
		if err := m.store.ReadJSON(path, &msg); err != nil {
			continue
		}
		if msg.Status == types.MessagePending {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// Acknowledge marks a message as acknowledged by the worker.
func (m *Mailbox) Acknowledge(teamName, workerID, messageID string) error {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	path := filepath.Join(m.paths.WorkerMailboxDir(teamName, workerID), messageID+".json")
	var msg types.MailboxMessage
	if err := m.store.ReadJSON(path, &msg); err != nil {
		return fmt.Errorf("read message: %w", err)
	}

	msg.Status = types.MessageAcknowledged
	return m.store.WriteJSON(path, &msg)
}

// DrainPending reads all pending messages, marks them as delivered,
// and returns the content formatted for prompt injection.
// This is the "enqueue-for-next-prompt" pattern.
func (m *Mailbox) DrainPending(teamName, workerID string) (string, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	dir := m.paths.WorkerMailboxDir(teamName, workerID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read mailbox dir: %w", err)
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var msg types.MailboxMessage
		if err := m.store.ReadJSON(path, &msg); err != nil {
			continue
		}
		if msg.Status != types.MessagePending {
			continue
		}

		parts = append(parts, fmt.Sprintf("[from %s]: %s", msg.From, msg.Content))

		msg.Status = types.MessageDelivered
		if err := m.store.WriteJSON(path, &msg); err != nil {
			continue
		}
	}

	if len(parts) == 0 {
		return "", nil
	}

	return FormatMailboxInjection(parts), nil
}

// FormatMailboxInjection formats messages for injection into a worker's prompt.
func FormatMailboxInjection(messages []string) string {
	if len(messages) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n--- Team Messages ---\n")
	for _, msg := range messages {
		b.WriteString(msg)
		b.WriteString("\n")
	}
	b.WriteString("--- End Team Messages ---\n")

	return b.String()
}
