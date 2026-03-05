package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/hub"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestHandleSSESetsHeadersAndSendsSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	provider := fakeSnapshotProvider{
		snapshot: orchestrator.StateSnapshot{
			Stats:       orchestrator.Stats{Running: 1},
			Running:     []orchestrator.RunningEntry{{IssueID: "issue-1"}},
			Backoff:     []types.BackoffEntry{},
			Issues:      map[string]types.Issue{"issue-1": {ID: "issue-1", Title: "Issue One"}},
			GeneratedAt: now,
		},
	}

	s, _, _, cleanup := newSSETestServer(t, provider)
	defer cleanup()

	resp, reader := mustOpenSSE(t, s.newMux())
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", resp.Header.Get("Connection"))
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))

	frame := readSSEFrame(t, reader)
	assert.Contains(t, frame, "event: snapshot")
	assert.Contains(t, frame, "id: 1")
	assert.Contains(t, frame, "retry: 1000")

	data := mustSSEData(t, frame)
	var snapshot orchestrator.StateSnapshot
	require.NoError(t, json.Unmarshal([]byte(data), &snapshot))
	assert.Equal(t, "issue-1", snapshot.Running[0].IssueID)
	assert.Equal(t, "Issue One", snapshot.Issues["issue-1"].Title)
}

func TestHandleSSEStreamsHubEvents(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s, source, _, cleanup := newSSETestServer(t, provider)
	defer cleanup()

	resp, reader := mustOpenSSE(t, s.newMux())
	defer resp.Body.Close()

	_ = readSSEFrame(t, reader)

	event := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   "issue-42",
		Data:      orchestrator.StatusUpdate{BackoffQueue: 3},
		Timestamp: time.Now().UTC(),
	}
	source <- event

	frame := readSSEFrame(t, reader)
	assert.Contains(t, frame, "event: StatusUpdate")
	assert.Contains(t, frame, "id: 2")
	assert.Contains(t, frame, "retry: 1000")

	data := mustSSEData(t, frame)
	var got struct {
		Type    orchestrator.EventType `json:"Type"`
		IssueID string                 `json:"IssueID"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &got))
	assert.Equal(t, event.Type, got.Type)
	assert.Equal(t, event.IssueID, got.IssueID)
}

func TestHandleSSEUnsubscribesOnClientDisconnect(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s, _, h, cleanup := newSSETestServer(t, provider)
	defer cleanup()

	resp, reader := mustOpenSSE(t, s.newMux())

	_ = readSSEFrame(t, reader)
	require.Eventually(t, func() bool {
		return h.SubscriberCount() == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		return h.SubscriberCount() == 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestHandleSSEReturns500WhenFlusherUnsupported(t *testing.T) {
	s := &Server{snapshotProvider: fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{}}, hub: hub.NewHub(make(chan orchestrator.OrchestratorEvent)), dashboardFS: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	w := &nonFlusherResponseWriter{header: make(http.Header)}

	s.handleSSE(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.status)
	assert.Contains(t, w.body.String(), "streaming unsupported")
}

func newSSETestServer(t *testing.T, provider fakeSnapshotProvider) (*Server, chan orchestrator.OrchestratorEvent, *hub.Hub, func()) {
	t.Helper()

	source := make(chan orchestrator.OrchestratorEvent, 1)
	h := hub.NewHub(source)
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	s := &Server{snapshotProvider: provider, hub: h, dashboardFS: nil}

	cleanup := func() {
		cancel()
	}

	return s, source, h, cleanup
}

func mustOpenSSE(t *testing.T, handler http.Handler) (*http.Response, *bufio.Reader) {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/events", nil)
	require.NoError(t, err)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	return resp, bufio.NewReader(resp.Body)
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) []string {
	t.Helper()

	type result struct {
		lines []string
		err   error
	}

	resultCh := make(chan result, 1)
	go func() {
		lines := make([]string, 0, 4)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				resultCh <- result{err: err}
				return
			}

			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				resultCh <- result{lines: lines}
				return
			}

			lines = append(lines, trimmed)
		}
	}()

	select {
	case res := <-resultCh:
		require.NoError(t, res.err)
		return res.lines
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE frame")
		return nil
	}
}

func mustSSEData(t *testing.T, frame []string) string {
	t.Helper()

	for _, line := range frame {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}

	t.Fatal("SSE frame missing data line")
	return ""
}

type nonFlusherResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *nonFlusherResponseWriter) Header() http.Header {
	return w.header
}

func (w *nonFlusherResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	return w.body.Write(data)
}

func (w *nonFlusherResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}
