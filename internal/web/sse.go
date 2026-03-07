package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const sseKeepAliveInterval = 15 * time.Second

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	if s.snapshotProvider == nil || s.hub == nil {
		writeJSONError(w, http.StatusInternalServerError, "server is not ready")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	subscriberID, events := s.hub.Subscribe()
	defer s.hub.Unsubscribe(subscriberID)

	id := int64(1)
	snapshot := s.snapshotProvider.Snapshot()
	snapshotGeneratedAt := snapshot.GeneratedAt
	if err := writeSSEEvent(w, "snapshot", snapshot, id); err != nil {
		return
	}
	flusher.Flush()
	id++

	ticker := time.NewTicker(sseKeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case evt, ok := <-events:
			if !ok {
				return
			}

			if shouldSkipStaleEvent(snapshotGeneratedAt, evt.Timestamp) {
				continue
			}

			if err := writeSSEEvent(w, evt.Type.String(), evt, id); err != nil {
				return
			}
			flusher.Flush()
			id++
		}
	}
}

func shouldSkipStaleEvent(snapshotGeneratedAt, eventTimestamp time.Time) bool {
	if snapshotGeneratedAt.IsZero() || eventTimestamp.IsZero() {
		return false
	}

	return !eventTimestamp.After(snapshotGeneratedAt)
}

func writeSSEEvent(w http.ResponseWriter, eventType string, payload interface{}, id int64) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\nid: %d\nretry: 1000\n\n", eventType, data, id)
	return err
}
