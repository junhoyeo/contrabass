package web

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentLogWriterImplementsIOWriter(t *testing.T) {
	var _ io.Writer = NewAgentLogWriter("worker-1", "stdout", make(chan WebEvent, 1))
}

func TestAgentLogWriterSingleCompleteLine(t *testing.T) {
	sink := make(chan WebEvent, 1)
	writer := NewAgentLogWriter("worker-1", "stdout", sink)

	n, err := writer.Write([]byte("hello world\n"))
	require.NoError(t, err)
	assert.Equal(t, len("hello world\n"), n)

	select {
	case event := <-sink:
		assert.Equal(t, WebEventAgentLog, event.Kind)
		assert.Equal(t, "agent_log", event.Type)
		payload, ok := event.Payload.(AgentLogEvent)
		require.True(t, ok)
		assert.Equal(t, "worker-1", payload.WorkerID)
		assert.Equal(t, "stdout", payload.Stream)
		assert.Equal(t, "hello world", payload.Line)
	default:
		t.Fatal("expected one event")
	}
}

func TestAgentLogWriterMultipleLinesInOneWrite(t *testing.T) {
	sink := make(chan WebEvent, 4)
	writer := NewAgentLogWriter("worker-2", "stderr", sink)

	n, err := writer.Write([]byte("first\nsecond\nthird\n"))
	require.NoError(t, err)
	assert.Equal(t, len("first\nsecond\nthird\n"), n)

	require.Equal(t, 3, len(sink))
	assertAgentLogEvent(t, <-sink, "worker-2", "stderr", "first")
	assertAgentLogEvent(t, <-sink, "worker-2", "stderr", "second")
	assertAgentLogEvent(t, <-sink, "worker-2", "stderr", "third")
}

func TestAgentLogWriterPartialLineBufferingAcrossWrites(t *testing.T) {
	sink := make(chan WebEvent, 3)
	writer := NewAgentLogWriter("worker-3", "stdout", sink)

	n, err := writer.Write([]byte("partial"))
	require.NoError(t, err)
	assert.Equal(t, len("partial"), n)
	assert.Equal(t, 0, len(sink))

	n, err = writer.Write([]byte(" line\nnext"))
	require.NoError(t, err)
	assert.Equal(t, len(" line\nnext"), n)

	require.Equal(t, 1, len(sink))
	assertAgentLogEvent(t, <-sink, "worker-3", "stdout", "partial line")

	n, err = writer.Write([]byte(" line\n"))
	require.NoError(t, err)
	assert.Equal(t, len(" line\n"), n)

	require.Equal(t, 1, len(sink))
	assertAgentLogEvent(t, <-sink, "worker-3", "stdout", "next line")
}

func TestAgentLogWriterEmptyWrite(t *testing.T) {
	sink := make(chan WebEvent, 1)
	writer := NewAgentLogWriter("worker-4", "stdout", sink)

	n, err := writer.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, len(sink))
}

func TestAgentLogWriterFullSinkIsNonBlocking(t *testing.T) {
	sink := make(chan WebEvent, 1)
	sink <- NewAgentLogWebEvent("existing", "already buffered", "stdout")
	writer := NewAgentLogWriter("worker-5", "stderr", sink)

	n, err := writer.Write([]byte("dropped line\n"))
	require.NoError(t, err)
	assert.Equal(t, len("dropped line\n"), n)

	assert.Equal(t, 1, len(sink))
	assertAgentLogEvent(t, <-sink, "existing", "stdout", "already buffered")
}

func assertAgentLogEvent(t *testing.T, event WebEvent, workerID string, stream string, line string) {
	t.Helper()

	assert.Equal(t, WebEventAgentLog, event.Kind)
	assert.Equal(t, "agent_log", event.Type)
	payload, ok := event.Payload.(AgentLogEvent)
	require.True(t, ok)
	assert.Equal(t, workerID, payload.WorkerID)
	assert.Equal(t, stream, payload.Stream)
	assert.Equal(t, line, payload.Line)
	assert.False(t, payload.Timestamp.IsZero())
	assert.False(t, event.Timestamp.IsZero())
}
