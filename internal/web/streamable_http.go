package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	streamableHTTPProtocolVersion = "2025-06-18"
	jsonRPCVersion                = "2.0"

	streamMethodSubscribe = "dashboard.subscribe"
	streamMethodSnapshot  = "dashboard.snapshot"
	streamMethodPing      = "dashboard.ping"

	mcpMethodInitialize  = "initialize"
	mcpMethodInitialized = "notifications/initialized"
	mcpMethodPing        = "ping"
	mcpMethodToolsList   = "tools/list"
	mcpMethodToolsCall   = "tools/call"

	mcpToolDashboardSnapshot = "contrabass.dashboard_snapshot"

	streamNotificationSnapshot = "dashboard.snapshot"
	streamNotificationEvent    = "dashboard.event"

	maxStreamableHTTPRequestBytes = 1 << 20
)

type streamableHTTPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type streamableHTTPMessage struct {
	JSONRPC string                  `json:"jsonrpc"`
	ID      json.RawMessage         `json:"id,omitempty"`
	Method  string                  `json:"method,omitempty"`
	Params  interface{}             `json:"params,omitempty"`
	Result  interface{}             `json:"result,omitempty"`
	Error   *streamableHTTPRPCError `json:"error,omitempty"`
}

type streamableHTTPRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

func (s *Server) handleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	if !isAllowedStreamOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "origin not allowed")
		return
	}

	switch r.Method {
	case http.MethodGet:
		if !acceptsResponseType(r, "text/event-stream") {
			writeJSONError(w, http.StatusNotAcceptable, "streamable HTTP GET requires Accept: text/event-stream")
			return
		}
		s.streamDashboardMessages(w, r, nil)
	case http.MethodPost:
		s.handleStreamableHTTPPost(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, OPTIONS")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleStreamableHTTPPost(w http.ResponseWriter, r *http.Request) {
	if !hasRequestContentType(r, "application/json") {
		writeJSONRPCError(w, http.StatusUnsupportedMediaType, nil, -32000, "streamable HTTP POST requires Content-Type: application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxStreamableHTTPRequestBytes)
	var req streamableHTTPRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}
	hasID := len(req.ID) > 0
	if req.JSONRPC != jsonRPCVersion || strings.TrimSpace(req.Method) == "" {
		writeJSONRPCError(w, http.StatusBadRequest, req.ID, -32600, "invalid request")
		return
	}
	if isMCPMethod(req.Method) && (r.URL.Path != mcpStreamPath || !s.isMCPStreamAuthorized(r)) {
		writeMCPUnauthorized(w, "invalid or expired MCP token")
		return
	}

	switch req.Method {
	case mcpMethodInitialize:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "initialize requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, mcpInitializeResult())
	case mcpMethodInitialized:
		if !hasID {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, map[string]interface{}{})
	case mcpMethodPing:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "ping requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, map[string]interface{}{})
	case mcpMethodToolsList:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "tools/list requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, s.mcpToolsListResult())
	case mcpMethodToolsCall:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "tools/call requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		s.handleMCPToolCall(w, req)
	case streamMethodSubscribe:
		if !acceptsResponseType(r, "text/event-stream") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "dashboard.subscribe requires Accept: text/event-stream")
			return
		}
		s.streamDashboardMessages(w, r, req.ID)
	case streamMethodSnapshot:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "dashboard.snapshot requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.snapshotProvider == nil {
			writeJSONRPCError(w, http.StatusInternalServerError, req.ID, -32000, "server is not ready")
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, s.snapshotProvider.Snapshot())
	case streamMethodPing:
		if !acceptsResponseType(r, "application/json") {
			writeJSONRPCError(w, http.StatusNotAcceptable, req.ID, -32000, "dashboard.ping requires Accept: application/json")
			return
		}
		if !hasID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, map[string]string{
			"transport":        "streamable_http",
			"protocol_version": streamableHTTPProtocolVersion,
		})
	default:
		if !hasID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSONRPCError(w, http.StatusOK, req.ID, -32601, "method not found")
	}
}

func isMCPMethod(method string) bool {
	switch method {
	case mcpMethodInitialize,
		mcpMethodInitialized,
		mcpMethodPing,
		mcpMethodToolsList,
		mcpMethodToolsCall:
		return true
	default:
		return false
	}
}

func mcpInitializeResult() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": streamableHTTPProtocolVersion,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]string{
			"name":    mcpServerName,
			"title":   "Contrabass",
			"version": "dev",
		},
		"instructions": "Use tools/list and tools/call to inspect the current Contrabass dashboard snapshot.",
	}
}

func (s *Server) mcpToolsListResult() map[string]interface{} {
	tools := []map[string]interface{}{}
	if s.supportsMCPDashboardSnapshot() {
		tools = append(tools,
			map[string]interface{}{
				"name":        mcpToolDashboardSnapshot,
				"title":       "Contrabass dashboard snapshot",
				"description": "Return the current Contrabass dashboard state snapshot as JSON.",
				"inputSchema": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
				},
			},
		)
	}
	return map[string]interface{}{"tools": tools}
}

func (s *Server) supportsMCPDashboardSnapshot() bool {
	provider, ok := s.snapshotProvider.(MCPDashboardSnapshotProvider)
	return !ok || provider.SupportsMCPDashboardSnapshot()
}

func (s *Server) handleMCPToolCall(w http.ResponseWriter, req streamableHTTPRequest) {
	var params mcpToolCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, http.StatusOK, req.ID, -32602, "invalid params")
			return
		}
	}

	switch params.Name {
	case mcpToolDashboardSnapshot:
		if !s.supportsMCPDashboardSnapshot() {
			writeJSONRPCError(w, http.StatusOK, req.ID, -32602, "unknown tool")
			return
		}
		if s.snapshotProvider == nil {
			writeJSONRPCError(w, http.StatusOK, req.ID, -32000, "server is not ready")
			return
		}
		payload, err := json.MarshalIndent(s.snapshotProvider.Snapshot(), "", "  ")
		if err != nil {
			writeJSONRPCError(w, http.StatusOK, req.ID, -32000, "failed to encode dashboard snapshot")
			return
		}
		writeJSONRPCResult(w, http.StatusOK, req.ID, map[string]interface{}{
			"content": []map[string]string{
				{
					"type": "text",
					"text": string(payload),
				},
			},
			"isError": false,
		})
	default:
		writeJSONRPCError(w, http.StatusOK, req.ID, -32602, "unknown tool")
	}
}

func (s *Server) streamDashboardMessages(w http.ResponseWriter, r *http.Request, requestID json.RawMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	if s.snapshotProvider == nil || s.hub == nil {
		writeJSONError(w, http.StatusInternalServerError, "server is not ready")
		return
	}

	if !s.isMCPStreamAuthorized(r) {
		writeMCPUnauthorized(w, "invalid or expired MCP token")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Mcp-Protocol-Version", streamableHTTPProtocolVersion)

	subscriberID, events := s.hub.Subscribe()
	defer s.hub.Unsubscribe(subscriberID)

	id := int64(1)
	if len(requestID) > 0 {
		if err := writeSSEEvent(w, "message", streamableHTTPMessage{
			JSONRPC: jsonRPCVersion,
			ID:      requestID,
			Result: map[string]string{
				"transport":        "streamable_http",
				"protocol_version": streamableHTTPProtocolVersion,
			},
		}, id); err != nil {
			return
		}
		flusher.Flush()
		id++
	}

	snapshot := s.snapshotProvider.Snapshot()
	snapshotGeneratedAt := snapshot.GeneratedAt
	if err := writeSSEEvent(w, "message", streamableHTTPMessage{
		JSONRPC: jsonRPCVersion,
		Method:  streamNotificationSnapshot,
		Params:  snapshot,
	}, id); err != nil {
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
			if !s.isMCPStreamAuthorized(r) {
				return
			}
			if _, err := fmt.Fprintf(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case webEvt, ok := <-events:
			if !ok {
				return
			}
			if shouldSkipStaleEvent(snapshotGeneratedAt, webEvt.Timestamp) {
				continue
			}
			if _, ok := sseEventChannel(webEvt); !ok {
				continue
			}
			if !s.isMCPStreamAuthorized(r) {
				return
			}
			if err := writeSSEEvent(w, "message", streamableHTTPMessage{
				JSONRPC: jsonRPCVersion,
				Method:  streamNotificationEvent,
				Params:  webEvt,
			}, id); err != nil {
				return
			}
			flusher.Flush()
			id++
		}
	}
}

func acceptsResponseType(r *http.Request, want string) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return want == "application/json"
	}

	for _, part := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if q, ok := params["q"]; ok {
			qValue, err := strconv.ParseFloat(q, 64)
			if err != nil || qValue <= 0 {
				continue
			}
		}
		if mediaType == "*/*" || mediaType == want {
			return true
		}
		if strings.HasSuffix(mediaType, "/*") && strings.HasPrefix(want, strings.TrimSuffix(mediaType, "*")) {
			return true
		}
	}
	return false
}

func hasRequestContentType(r *http.Request, want string) bool {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == want
}

func isAllowedStreamOrigin(r *http.Request) bool {
	return isAllowedLoopbackExactOrigin(r)
}

func hostWithoutPort(host string) string {
	hostname, _, err := net.SplitHostPort(host)
	if err == nil {
		return hostname
	}
	return host
}

func isLoopbackHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), "[]")
	if normalized == "localhost" {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func writeJSONRPCResult(w http.ResponseWriter, status int, id json.RawMessage, result interface{}) {
	writeJSONRPCMessage(w, status, streamableHTTPMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
}

func writeJSONRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSONRPCMessage(w, status, streamableHTTPMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &streamableHTTPRPCError{
			Code:    code,
			Message: message,
		},
	})
}

func writeJSONRPCMessage(w http.ResponseWriter, status int, message streamableHTTPMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Protocol-Version", streamableHTTPProtocolVersion)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(message); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		return
	}
}
