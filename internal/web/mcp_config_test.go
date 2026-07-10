package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestHandleGetMCPConfigReturnsCopyableAgentConfig(t *testing.T) {
	s := &Server{snapshotProvider: fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}}
	req := httptest.NewRequest(http.MethodGet, "http://internal/api/v1/mcp/config", nil)
	req.Host = "localhost:9090"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var payload mcpConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, mcpServerName, payload.ServerName)
	assert.Equal(t, mcpTransportType, payload.Transport)
	assert.Equal(t, streamableHTTPProtocolVersion, payload.ProtocolVersion)
	assert.True(t, payload.TokenRequired)
	assert.Empty(t, payload.Token)
	assert.Nil(t, payload.ExpiresAt)
	assert.Equal(t, "http://localhost:9090/api/v1/mcp/stream", payload.URL)
	assert.Equal(t, "http://localhost:9090/api/v1/mcp/token", payload.RegenerateEndpoint)

	server, ok := payload.Config.MCPServers[mcpServerName]
	require.True(t, ok)
	assert.Equal(t, mcpTransportType, server.Type)
	assert.Equal(t, payload.URL, server.URL)
	assert.Empty(t, server.Headers)
}

func TestHandleCreateMCPTokenReturnsTokenizedAgentConfig(t *testing.T) {
	s := &Server{snapshotProvider: fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/mcp/token", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var payload mcpConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Token)
	assert.True(t, len(payload.Token) > len("mcp_"))
	assert.Equal(t, "Bearer "+payload.Token, payload.Authorization)
	require.NotNil(t, payload.ExpiresAt)
	assert.True(t, payload.ExpiresAt.After(time.Now().UTC()))
	assert.Greater(t, payload.ExpiresInSeconds, int64(0))

	server := payload.Config.MCPServers[mcpServerName]
	require.NotNil(t, server.Headers)
	assert.Equal(t, "Bearer "+payload.Token, server.Headers["Authorization"])
}

func TestHandleCreateMCPTokenRequiresLocalSameOriginRequest(t *testing.T) {
	s := &Server{snapshotProvider: fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}}

	t.Run("rejects cross origin browser request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/mcp/token", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", "http://localhost:5173")
		rec := httptest.NewRecorder()

		s.newMux().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("allows exact same origin local request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/mcp/token", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", "http://localhost:8080")
		rec := httptest.NewRecorder()

		s.newMux().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "http://localhost:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("rejects non loopback host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://contrabass.example/api/v1/mcp/token", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", "http://contrabass.example")
		rec := httptest.NewRecorder()

		s.newMux().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestMCPStreamRequiresGeneratedToken(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 1},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}
	body := []byte(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`)

	req := newLocalWebRequest(http.MethodPost, "/api/v1/mcp/stream", bytes.NewReader(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")

	req = newLocalWebRequest(http.MethodPost, "/api/v1/stream", bytes.NewReader(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "legacy dashboard stream must remain tokenless")
}

func TestMCPStreamAcceptsGeneratedToken(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{
		Stats:       orchestrator.Stats{Running: 1},
		Issues:      map[string]types.Issue{},
		GeneratedAt: time.Now().UTC(),
	}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/token", nil)
	tokenReq.Host = "localhost:8080"
	tokenReq.RemoteAddr = "127.0.0.1:12345"
	tokenRec := httptest.NewRecorder()
	s.newMux().ServeHTTP(tokenRec, tokenReq)
	require.Equal(t, http.StatusCreated, tokenRec.Code)
	var config mcpConfigResponse
	require.NoError(t, json.Unmarshal(tokenRec.Body.Bytes(), &config))
	require.NotEmpty(t, config.Token)

	req := newLocalWebRequest(http.MethodPost, "/api/v1/mcp/stream", bytes.NewBufferString(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.Token)
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var message streamableHTTPMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	assert.Equal(t, json.RawMessage(`"ping-1"`), message.ID)
	require.NotNil(t, message.Result)
}

func TestMCPTokenAuthAddsExpiryBoundContextAndRevalidatesRotation(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	token := "mcp_test_token"
	s := &Server{mcpTokens: map[string]mcpTokenRecord{
		token: {
			CreatedAt: time.Now().UTC(),
			ExpiresAt: expiresAt,
		},
	}}
	req := newLocalWebRequest(http.MethodPost, "/api/v1/mcp/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	var gotDeadline time.Time
	var hasDeadline bool
	var authorizedBeforeRotation bool
	var authorizedAfterRotation bool
	s.withMCPTokenAuth(func(w http.ResponseWriter, r *http.Request) {
		gotDeadline, hasDeadline = r.Context().Deadline()
		authorizedBeforeRotation = s.isMCPStreamAuthorized(r)
		s.mcpTokenMu.Lock()
		s.mcpTokens = map[string]mcpTokenRecord{}
		s.mcpTokenMu.Unlock()
		authorizedAfterRotation = s.isMCPStreamAuthorized(r)
		w.WriteHeader(http.StatusNoContent)
	})(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, hasDeadline)
	assert.WithinDuration(t, expiresAt, gotDeadline, time.Second)
	assert.True(t, authorizedBeforeRotation)
	assert.False(t, authorizedAfterRotation)
}

func TestMCPStreamAuthorizationRequiresAuthContextForMCPPath(t *testing.T) {
	s := &Server{}

	assert.False(t, s.isMCPStreamAuthorized(newLocalWebRequest(http.MethodPost, mcpStreamPath, nil)))
	assert.True(t, s.isMCPStreamAuthorized(newLocalWebRequest(http.MethodPost, "/api/v1/stream", nil)))
}

func TestCreateMCPTokenRotatesPreviousToken(t *testing.T) {
	s := &Server{snapshotProvider: fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}}

	first, _, err := s.createMCPToken(time.Now().UTC())
	require.NoError(t, err)
	require.True(t, s.isValidMCPToken(first, time.Now().UTC()))

	second, _, err := s.createMCPToken(time.Now().UTC())
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	assert.False(t, s.isValidMCPToken(first, time.Now().UTC()))
	assert.True(t, s.isValidMCPToken(second, time.Now().UTC()))
}
