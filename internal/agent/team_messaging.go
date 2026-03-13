package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// cliMailboxMessage is the JSON shape returned by the team CLI mailbox API.
type cliMailboxMessage struct {
	MessageID   string    `json:"message_id"`
	FromWorker  string    `json:"from_worker"`
	ToWorker    string    `json:"to_worker"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	NotifiedAt  time.Time `json:"notified_at,omitempty"`
	DeliveredAt time.Time `json:"delivered_at,omitempty"`
}

func (m *cliMailboxMessage) toMailboxMessage() types.MailboxMessage {
	status := types.MessagePending
	if !m.DeliveredAt.IsZero() {
		status = types.MessageDelivered
	} else if !m.NotifiedAt.IsZero() {
		status = types.MessageDelivered
	}
	return types.MailboxMessage{
		ID:        m.MessageID,
		From:      m.FromWorker,
		To:        m.ToWorker,
		Content:   m.Body,
		Timestamp: m.CreatedAt,
		Status:    status,
	}
}

func convertCLIMessages(msgs []cliMailboxMessage) []types.MailboxMessage {
	result := make([]types.MailboxMessage, len(msgs))
	for i, m := range msgs {
		result[i] = m.toMailboxMessage()
	}
	return result
}

// SendMessage sends a direct message from one worker to another.
func (r *teamCLIRunner) SendMessage(ctx context.Context, workspace, teamName, fromWorker, toWorker, body string) (*types.MailboxMessage, error) {
	if fromWorker == "" {
		return nil, fmt.Errorf("from_worker is required")
	}
	if toWorker == "" {
		return nil, fmt.Errorf("to_worker is required")
	}
	if body == "" {
		return nil, fmt.Errorf("message body is required")
	}

	input := map[string]string{
		"team_name":   teamName,
		"from_worker": fromWorker,
		"to_worker":   toWorker,
		"body":        body,
	}

	var resp struct {
		Message cliMailboxMessage `json:"message"`
	}

	if err := r.runTeamAPI(ctx, workspace, "send-message", input, &resp); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}

	msg := resp.Message.toMailboxMessage()
	return &msg, nil
}

// BroadcastMessage sends a message from one worker to all other workers.
func (r *teamCLIRunner) BroadcastMessage(ctx context.Context, workspace, teamName, fromWorker, body string) ([]types.MailboxMessage, error) {
	if fromWorker == "" {
		return nil, fmt.Errorf("from_worker is required")
	}
	if body == "" {
		return nil, fmt.Errorf("message body is required")
	}

	input := map[string]string{
		"team_name":   teamName,
		"from_worker": fromWorker,
		"body":        body,
	}

	var resp struct {
		Count    int                 `json:"count"`
		Messages []cliMailboxMessage `json:"messages"`
	}

	if err := r.runTeamAPI(ctx, workspace, "broadcast", input, &resp); err != nil {
		return nil, fmt.Errorf("broadcast message: %w", err)
	}

	return convertCLIMessages(resp.Messages), nil
}

// ListMailbox lists messages in a worker's mailbox.
func (r *teamCLIRunner) ListMailbox(ctx context.Context, workspace, teamName, worker string, includeDelivered bool) ([]types.MailboxMessage, error) {
	if worker == "" {
		return nil, fmt.Errorf("worker is required")
	}

	input := map[string]interface{}{
		"team_name":         teamName,
		"worker":            worker,
		"include_delivered": includeDelivered,
	}

	var resp struct {
		Worker   string              `json:"worker"`
		Count    int                 `json:"count"`
		Messages []cliMailboxMessage `json:"messages"`
	}

	if err := r.runTeamAPI(ctx, workspace, "mailbox-list", input, &resp); err != nil {
		return nil, fmt.Errorf("list mailbox: %w", err)
	}

	return convertCLIMessages(resp.Messages), nil
}

// MarkMessageDelivered marks a message as delivered.
func (r *teamCLIRunner) MarkMessageDelivered(ctx context.Context, workspace, teamName, worker, messageID string) error {
	if worker == "" {
		return fmt.Errorf("worker is required")
	}
	if messageID == "" {
		return fmt.Errorf("message_id is required")
	}

	input := map[string]string{
		"team_name":  teamName,
		"worker":     worker,
		"message_id": messageID,
	}

	if err := r.runTeamAPI(ctx, workspace, "mailbox-mark-delivered", input, nil); err != nil {
		return fmt.Errorf("mark message delivered: %w", err)
	}
	return nil
}

// MarkMessageNotified marks a message as notified.
func (r *teamCLIRunner) MarkMessageNotified(ctx context.Context, workspace, teamName, worker, messageID string) error {
	if worker == "" {
		return fmt.Errorf("worker is required")
	}
	if messageID == "" {
		return fmt.Errorf("message_id is required")
	}

	input := map[string]string{
		"team_name":  teamName,
		"worker":     worker,
		"message_id": messageID,
	}

	if err := r.runTeamAPI(ctx, workspace, "mailbox-mark-notified", input, nil); err != nil {
		return fmt.Errorf("mark message notified: %w", err)
	}
	return nil
}

// GetUnreadMessages returns undelivered messages for a worker.
func (r *teamCLIRunner) GetUnreadMessages(ctx context.Context, workspace, teamName, worker string) ([]types.MailboxMessage, error) {
	messages, err := r.ListMailbox(ctx, workspace, teamName, worker, false)
	if err != nil {
		return nil, err
	}

	var unread []types.MailboxMessage
	for _, msg := range messages {
		if msg.Status == types.MessagePending {
			unread = append(unread, msg)
		}
	}
	return unread, nil
}
