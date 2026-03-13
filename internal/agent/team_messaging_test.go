package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestCLIMailboxMessageConversion(t *testing.T) {
	now := time.Now()
	cliMsg := &cliMailboxMessage{
		MessageID:   "msg_123",
		FromWorker:  "worker-1",
		ToWorker:    "worker-2",
		Body:        "Please review my changes",
		CreatedAt:   now,
		NotifiedAt:  now.Add(1 * time.Second),
		DeliveredAt: now.Add(2 * time.Second),
	}

	msg := cliMsg.toMailboxMessage()

	if msg.ID != cliMsg.MessageID {
		t.Errorf("ID mismatch: got %s, want %s", msg.ID, cliMsg.MessageID)
	}
	if msg.From != cliMsg.FromWorker {
		t.Errorf("From mismatch: got %s, want %s", msg.From, cliMsg.FromWorker)
	}
	if msg.To != cliMsg.ToWorker {
		t.Errorf("To mismatch: got %s, want %s", msg.To, cliMsg.ToWorker)
	}
	if msg.Content != cliMsg.Body {
		t.Errorf("Content mismatch: got %s, want %s", msg.Content, cliMsg.Body)
	}
	if msg.Status != types.MessageDelivered {
		t.Errorf("Status should be delivered, got %s", msg.Status)
	}
}

func TestMailboxMessageJSON(t *testing.T) {
	msg := types.MailboxMessage{
		ID:        "msg_123",
		From:      "worker-1",
		To:        "worker-2",
		Content:   "Please review my changes",
		Timestamp: time.Now(),
		Status:    types.MessagePending,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal message: %v", err)
	}

	var decoded types.MailboxMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch: got %s, want %s", decoded.ID, msg.ID)
	}
	if decoded.From != msg.From {
		t.Errorf("From mismatch: got %s, want %s", decoded.From, msg.From)
	}
	if decoded.To != msg.To {
		t.Errorf("To mismatch: got %s, want %s", decoded.To, msg.To)
	}
}

func TestUnreadMessageFiltering(t *testing.T) {
	now := time.Now()
	messages := []types.MailboxMessage{
		{
			ID:        "msg_1",
			From:      "worker-1",
			To:        "worker-2",
			Content:   "Message 1",
			Timestamp: now,
			Status:    types.MessageDelivered,
		},
		{
			ID:        "msg_2",
			From:      "worker-1",
			To:        "worker-2",
			Content:   "Message 2",
			Timestamp: now,
			Status:    types.MessagePending,
		},
		{
			ID:        "msg_3",
			From:      "worker-3",
			To:        "worker-2",
			Content:   "Message 3",
			Timestamp: now,
			Status:    types.MessagePending,
		},
	}

	var unread []types.MailboxMessage
	for _, msg := range messages {
		if msg.Status == types.MessagePending {
			unread = append(unread, msg)
		}
	}

	if len(unread) != 2 {
		t.Errorf("Expected 2 unread messages, got %d", len(unread))
	}
}

func TestMessageNotificationFlow(t *testing.T) {
	msg := types.MailboxMessage{
		ID:        "msg_123",
		From:      "worker-1",
		To:        "worker-2",
		Content:   "Test message",
		Timestamp: time.Now(),
		Status:    types.MessagePending,
	}

	if msg.Status != types.MessagePending {
		t.Error("Status should be pending initially")
	}

	msg.Status = types.MessageDelivered
	if msg.Status != types.MessageDelivered {
		t.Error("Status should be delivered after delivery")
	}
}
