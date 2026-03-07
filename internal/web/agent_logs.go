package web

import (
	"bytes"
	"io"
)

var _ io.Writer = (*AgentLogWriter)(nil)

type AgentLogWriter struct {
	workerID string
	stream   string
	sink     chan<- WebEvent
	buf      []byte
}

func NewAgentLogWriter(workerID, stream string, sink chan<- WebEvent) *AgentLogWriter {
	return &AgentLogWriter{
		workerID: workerID,
		stream:   stream,
		sink:     sink,
	}
}

func (w *AgentLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)

	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}

		line := string(w.buf[:idx])
		event := NewAgentLogWebEvent(w.workerID, line, w.stream)
		select {
		case w.sink <- event:
		default:
		}

		w.buf = w.buf[idx+1:]
	}

	return len(p), nil
}
