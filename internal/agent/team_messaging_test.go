package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	assert.Equal(t, cliMsg.MessageID, msg.ID)
	assert.Equal(t, cliMsg.FromWorker, msg.From)
	assert.Equal(t, cliMsg.ToWorker, msg.To)
	assert.Equal(t, cliMsg.Body, msg.Content)
	assert.Equal(t, types.MessageAcknowledged, msg.Status)
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
	require.NoError(t, err)

	var decoded types.MailboxMessage
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, msg.ID, decoded.ID)
	assert.Equal(t, msg.From, decoded.From)
	assert.Equal(t, msg.To, decoded.To)
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

	assert.Len(t, unread, 2)
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

	assert.Equal(t, types.MessagePending, msg.Status)

	msg.Status = types.MessageDelivered
	assert.Equal(t, types.MessageDelivered, msg.Status)
}
