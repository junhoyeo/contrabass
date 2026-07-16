package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	mcpServerName      = "contrabass"
	mcpStreamPath      = "/api/v1/mcp/stream"
	mcpTokenByteLength = 32
	mcpTokenTTL        = 24 * time.Hour
	mcpTransportType   = "streamable_http"
)

type mcpTokenRecord struct {
	CreatedAt time.Time
	ExpiresAt time.Time
}

type mcpTokenAuthContextKey struct{}

type mcpTokenAuthContext struct {
	Token     string
	ExpiresAt time.Time
}

type mcpConfigResponse struct {
	ServerName         string         `json:"server_name"`
	Transport          string         `json:"transport"`
	URL                string         `json:"url"`
	ProtocolVersion    string         `json:"protocol_version"`
	TokenRequired      bool           `json:"token_required"`
	Token              string         `json:"token,omitempty"`
	Authorization      string         `json:"authorization_header,omitempty"`
	ExpiresAt          *time.Time     `json:"expires_at,omitempty"`
	GeneratedAt        time.Time      `json:"generated_at"`
	Config             mcpAgentConfig `json:"config"`
	ExpiresInSeconds   int64          `json:"expires_in_seconds,omitempty"`
	RegenerateEndpoint string         `json:"regenerate_endpoint"`
}

type mcpAgentConfig struct {
	MCPServers map[string]mcpAgentServerConfig `json:"mcpServers"`
}

type mcpAgentServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (s *Server) handleGetMCPConfig(w http.ResponseWriter, r *http.Request) {
	if !isLocalMCPAdminRequest(r) {
		writeJSONError(w, http.StatusForbidden, "MCP config is only available from the local dashboard origin")
		return
	}

	writeJSON(w, http.StatusOK, newMCPConfigResponse(r, "", nil))
}

func (s *Server) handleCreateMCPToken(w http.ResponseWriter, r *http.Request) {
	if !isLocalMCPAdminRequest(r) {
		writeJSONError(w, http.StatusForbidden, "MCP token generation is only available from the local dashboard origin")
		return
	}

	token, expiresAt, err := s.createMCPToken(time.Now().UTC())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate MCP token")
		return
	}

	writeJSON(w, http.StatusCreated, newMCPConfigResponse(r, token, &expiresAt))
}

func (s *Server) withMCPTokenAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			writeMCPUnauthorized(w, "missing MCP token")
			return
		}
		expiresAt, ok := s.validMCPTokenExpiry(token, time.Now().UTC())
		if !ok {
			writeMCPUnauthorized(w, "invalid or expired MCP token")
			return
		}

		ctx, cancel := context.WithDeadline(r.Context(), expiresAt)
		defer cancel()
		ctx = context.WithValue(ctx, mcpTokenAuthContextKey{}, mcpTokenAuthContext{
			Token:     token,
			ExpiresAt: expiresAt,
		})

		next(w, r.WithContext(ctx))
	}
}

func (s *Server) createMCPToken(now time.Time) (string, time.Time, error) {
	raw := make([]byte, mcpTokenByteLength)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}

	token := "mcp_" + base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := now.Add(mcpTokenTTL)

	s.mcpTokenMu.Lock()
	defer s.mcpTokenMu.Unlock()
	if s.mcpTokens == nil {
		s.mcpTokens = make(map[string]mcpTokenRecord)
	}

	// The dashboard exposes one current copyable MCP config. Treat a new token
	// as rotation so old copied configs are revoked and the in-memory store stays
	// bounded even if the button is clicked repeatedly.
	s.mcpTokens = map[string]mcpTokenRecord{token: {
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}}

	return token, expiresAt, nil
}

func (s *Server) isValidMCPToken(token string, now time.Time) bool {
	_, ok := s.validMCPTokenExpiry(token, now)
	return ok
}

func (s *Server) validMCPTokenExpiry(token string, now time.Time) (time.Time, bool) {
	s.mcpTokenMu.Lock()
	defer s.mcpTokenMu.Unlock()

	if s.mcpTokens == nil {
		return time.Time{}, false
	}
	record, ok := s.mcpTokens[token]
	if !ok {
		return time.Time{}, false
	}
	if !record.ExpiresAt.After(now) {
		delete(s.mcpTokens, token)
		return time.Time{}, false
	}
	return record.ExpiresAt, true
}

func mcpTokenAuthFromContext(ctx context.Context) (mcpTokenAuthContext, bool) {
	auth, ok := ctx.Value(mcpTokenAuthContextKey{}).(mcpTokenAuthContext)
	return auth, ok
}

func (s *Server) isMCPStreamAuthorized(r *http.Request) bool {
	auth, ok := mcpTokenAuthFromContext(r.Context())
	if !ok {
		return r.URL.Path != mcpStreamPath
	}
	return s.isValidMCPToken(auth.Token, time.Now().UTC())
}

func newMCPConfigResponse(r *http.Request, token string, expiresAt *time.Time) mcpConfigResponse {
	streamURL := publicURLForPath(r, mcpStreamPath)
	server := mcpAgentServerConfig{
		Type: mcpTransportType,
		URL:  streamURL,
	}
	authorization := ""
	if token != "" {
		authorization = "Bearer " + token
		server.Headers = map[string]string{"Authorization": authorization}
	}

	resp := mcpConfigResponse{
		ServerName:         mcpServerName,
		Transport:          mcpTransportType,
		URL:                streamURL,
		ProtocolVersion:    streamableHTTPProtocolVersion,
		TokenRequired:      true,
		Token:              token,
		Authorization:      authorization,
		ExpiresAt:          expiresAt,
		GeneratedAt:        time.Now().UTC(),
		Config:             mcpAgentConfig{MCPServers: map[string]mcpAgentServerConfig{mcpServerName: server}},
		RegenerateEndpoint: publicURLForPath(r, "/api/v1/mcp/token"),
	}
	if expiresAt != nil {
		resp.ExpiresInSeconds = int64(time.Until(*expiresAt).Seconds())
	}
	return resp
}

func publicURLForPath(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = defaultListenAddr
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return scheme + "://" + host + path
}

func bearerToken(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	if authorization == "" {
		return ""
	}
	scheme, token, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func writeMCPUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="contrabass-mcp"`)
	writeJSONError(w, http.StatusUnauthorized, message)
}

func (s *Server) withMCPAdminCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedExactOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "origin not allowed")
			return
		}

		setExactCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func (s *Server) withMCPStreamCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedLoopbackExactOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "origin not allowed")
			return
		}

		setExactCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func (s *Server) withDashboardStreamCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedLoopbackExactOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "origin not allowed")
			return
		}

		setExactCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func setExactCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Mcp-Protocol-Version")
}

func isLocalMCPAdminRequest(r *http.Request) bool {
	return isAllowedLoopbackExactOrigin(r)
}

func isAllowedLoopbackExactOrigin(r *http.Request) bool {
	return isLoopbackHost(hostWithoutPort(r.Host)) && isLoopbackHost(remoteHost(r.RemoteAddr)) && isAllowedExactOrigin(r)
}

func isAllowedExactOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return false
	}

	for _, prefix := range []string{"http://", "https://"} {
		if strings.EqualFold(origin, prefix+host) {
			return true
		}
	}
	return false
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		return host
	}
	return hostWithoutPort(remoteAddr)
}
