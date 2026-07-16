package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestHandleStreamableHTTPPostStreamsSnapshotAndHubEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 1},
		Running:     []orchestrator.RunningEntry{{IssueID: "issue-1"}},
		Backoff:     []types.BackoffEntry{},
		Issues:      map[string]types.Issue{"issue-1": {ID: "issue-1", Title: "Issue One"}},
		GeneratedAt: now,
	}}
	s, source, _, cleanup := newSSETestServer(t, provider)
	defer cleanup()

	resp, reader := mustOpenStreamablePOST(t, s.newMux(), `{"jsonrpc":"2.0","id":"sub-1","method":"dashboard.subscribe"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, streamableHTTPProtocolVersion, resp.Header.Get("Mcp-Protocol-Version"))

	ackFrame := readSSEFrame(t, reader)
	ackMessage := mustStreamableMessage(t, ackFrame)
	assert.Equal(t, "2.0", ackMessage.JSONRPC)
	assert.Equal(t, json.RawMessage(`"sub-1"`), ackMessage.ID)
	require.NotNil(t, ackMessage.Result)

	snapshotFrame := readSSEFrame(t, reader)
	snapshotMessage := mustStreamableMessage(t, snapshotFrame)
	assert.Equal(t, streamNotificationSnapshot, snapshotMessage.Method)
	params, ok := snapshotMessage.Params.(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, params, "stats")
	assert.Contains(t, params, "running")

	orchEvent := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   "issue-42",
		Data:      orchestrator.StatusUpdate{BackoffQueue: 3},
		Timestamp: now.Add(time.Second),
	}
	source <- NewOrchestratorWebEvent(orchEvent)

	eventFrame := readSSEFrame(t, reader)
	eventMessage := mustStreamableMessage(t, eventFrame)
	assert.Equal(t, streamNotificationEvent, eventMessage.Method)
	webEvent, ok := eventMessage.Params.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, string(WebEventOrchestrator), webEvent["kind"])
	assert.Equal(t, "StatusUpdate", webEvent["type"])
}

func TestStreamableHTTPRevalidatesMCPTokenBeforeSubscribeAck(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 1},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s, _, _, cleanup := newSSETestServer(t, provider)
	defer cleanup()
	token := "mcp_rotated_before_ack"
	s.mcpTokens = map[string]mcpTokenRecord{token: {
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/mcp/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"sub-1","method":"dashboard.subscribe"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.withMCPTokenAuth(func(w http.ResponseWriter, r *http.Request) {
		s.mcpTokenMu.Lock()
		s.mcpTokens = map[string]mcpTokenRecord{}
		s.mcpTokenMu.Unlock()
		s.handleStreamableHTTP(w, r)
	})(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "streamable_http")
}

func TestHandleMCPInitializeAndTools(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats: orchestrator.Stats{
			Running:        2,
			MaxAgents:      4,
			TotalTokensIn:  10,
			TotalTokensOut: 5,
			StartTime:      time.Now().UTC(),
			PollCount:      1,
		},
		Running:     []orchestrator.RunningEntry{},
		Backoff:     []types.BackoffEntry{},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}
	token, _, err := s.createMCPToken(time.Now().UTC())
	require.NoError(t, err)
	h := s.newMux()

	initialize := mustPostMCPJSONRPC(t, h, token, `{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`)
	assert.Equal(t, http.StatusOK, initialize.Code)
	initMessage := mustJSONRPCMessage(t, initialize)
	initResult, ok := initMessage.Result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, streamableHTTPProtocolVersion, initResult["protocolVersion"])
	assert.Contains(t, initResult, "capabilities")
	assert.Contains(t, initResult, "serverInfo")

	initialized := mustPostMCPJSONRPC(t, h, token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	assert.Equal(t, http.StatusAccepted, initialized.Code)
	assert.Empty(t, initialized.Body.String())

	tools := mustPostMCPJSONRPC(t, h, token, `{"jsonrpc":"2.0","id":"tools-1","method":"tools/list"}`)
	assert.Equal(t, http.StatusOK, tools.Code)
	toolsMessage := mustJSONRPCMessage(t, tools)
	toolsResult, ok := toolsMessage.Result.(map[string]interface{})
	require.True(t, ok)
	toolList, ok := toolsResult["tools"].([]interface{})
	require.True(t, ok)
	require.Len(t, toolList, 1)
	tool, ok := toolList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, mcpToolDashboardSnapshot, tool["name"])

	call := mustPostMCPJSONRPC(t, h, token, `{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"contrabass.dashboard_snapshot","arguments":{}}}`)
	assert.Equal(t, http.StatusOK, call.Code)
	callMessage := mustJSONRPCMessage(t, call)
	callResult, ok := callMessage.Result.(map[string]interface{})
	require.True(t, ok)
	content, ok := callResult["content"].([]interface{})
	require.True(t, ok)
	require.Len(t, content, 1)
	textContent, ok := content[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "text", textContent["type"])
	assert.Contains(t, textContent["text"], `"stats"`)
}

func TestHandleMCPMethodsRequireTokenProtectedMCPPath(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 1},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"contrabass.dashboard_snapshot","arguments":{}}}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"stats"`)
}

func TestHandleStreamableHTTPGetStreamsSnapshot(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 2},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s, _, _, cleanup := newSSETestServer(t, provider)
	defer cleanup()

	resp, reader := mustOpenStreamableGET(t, s.newMux())
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	frame := readSSEFrame(t, reader)
	message := mustStreamableMessage(t, frame)
	assert.Equal(t, streamNotificationSnapshot, message.Method)
}

func TestHandleStreamableHTTPSnapshotReturnsJSONRPCResult(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 3},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"state-1","method":"dashboard.snapshot"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	assert.Equal(t, json.RawMessage(`"state-1"`), message.ID)
	require.NotNil(t, message.Result)
}

func TestHandleStreamableHTTPPostRequiresJSONContentType(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.NotNil(t, message.Error)
	assert.Contains(t, message.Error.Message, "Content-Type")
}

func TestHandleStreamableHTTPPostRejectsOversizedBody(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBuffer(bytes.Repeat([]byte("x"), maxStreamableHTTPRequestBytes+1)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.NotNil(t, message.Error)
	assert.Equal(t, -32700, message.Error.Code)
}

func TestHandleStreamableHTTPPostRejectsTrailingJSON(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"} {}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.NotNil(t, message.Error)
	assert.Equal(t, -32700, message.Error.Code)
}

func TestHandleStreamableHTTPNotificationWithoutIDDoesNotReturnJSONRPCResponse(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 3},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","method":"dashboard.ping"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestHandleStreamableHTTPPostRequiresStreamAcceptForSubscribe(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"sub-1","method":"dashboard.subscribe"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotAcceptable, rec.Code)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.NotNil(t, message.Error)
	assert.Contains(t, message.Error.Message, "text/event-stream")
}

func TestHandleStreamableHTTPAcceptHonorsQZero(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	tests := []struct {
		name   string
		accept string
		body   string
	}{
		{
			name:   "json rejected for ping",
			accept: "application/json;q=0, text/event-stream;q=1",
			body:   `{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`,
		},
		{
			name:   "stream rejected for subscribe",
			accept: "text/event-stream;q=0, application/json;q=1",
			body:   `{"jsonrpc":"2.0","id":"sub-1","method":"dashboard.subscribe"}`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewBufferString(tt.body))
			req.Header.Set("Accept", tt.accept)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			s.newMux().ServeHTTP(rec, req)

			assert.Equal(t, http.StatusNotAcceptable, rec.Code)
		})
	}
}

func TestHandleStreamableHTTPRejectsCrossLoopbackOrigin(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleStreamableHTTPRejectsReboundHostEvenWhenOriginMatches(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	req := httptest.NewRequest(http.MethodPost, "http://evil.example/api/v1/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func newLocalWebRequest(method string, target string, body io.Reader) *http.Request {
	if strings.HasPrefix(target, "/") {
		target = "http://localhost:8080" + target
	}
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:12345"
	return req
}

func mustOpenStreamablePOST(t *testing.T, handler http.Handler, body string) (*http.Response, *bufio.Reader) {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/stream", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Protocol-Version", streamableHTTPProtocolVersion)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	return resp, bufio.NewReader(resp.Body)
}

func mustOpenStreamableGET(t *testing.T, handler http.Handler) (*http.Response, *bufio.Reader) {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", streamableHTTPProtocolVersion)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	return resp, bufio.NewReader(resp.Body)
}

func mustStreamableMessage(t *testing.T, frame []string) streamableHTTPMessage {
	t.Helper()

	data := mustSSEData(t, frame)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal([]byte(data), &message))
	return message
}

func mustPostMCPJSONRPC(t *testing.T, handler http.Handler, token string, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := newLocalWebRequest(http.MethodPost, mcpStreamPath, bytes.NewBufferString(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func mustJSONRPCMessage(t *testing.T, rec *httptest.ResponseRecorder) streamableHTTPMessage {
	t.Helper()

	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.Nil(t, message.Error)
	return message
}
