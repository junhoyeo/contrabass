package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/hub"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
)

const defaultListenAddr = "localhost:8080"

// allowedOriginsEnv is the escape hatch for serving the dashboard from a
// different origin (e.g. behind a reverse proxy): a comma-separated list of
// exact origins such as "https://dash.example.com", or "*" to disable the
// origin/Host checks entirely. Same-origin and loopback origins are always
// allowed without configuration.
const allowedOriginsEnv = "CONTRABASS_ALLOWED_ORIGINS"

// readHeaderTimeout bounds slowloris-style clients that trickle header bytes;
// it must not bound the response so SSE streams stay open indefinitely.
const readHeaderTimeout = 10 * time.Second

type SnapshotProvider interface {
	Snapshot() orchestrator.StateSnapshot
}

// MCPDashboardSnapshotProvider reports whether the generic orchestrator
// snapshot tool is meaningful for this server's current snapshot source.
// Providers that do not implement it retain the legacy supported behavior.
type MCPDashboardSnapshotProvider interface {
	SupportsMCPDashboardSnapshot() bool
}

// AgentStopper is implemented by the orchestrator so the dashboard can
// terminate a running agent via the stop endpoint without coupling the
// HTTP layer to process-lifecycle details.
type AgentStopper interface {
	StopAgent(ctx context.Context, issueID string) error
}

type BoardProvider interface {
	ListIssues(ctx context.Context, includeDone bool) ([]tracker.LocalBoardIssue, error)
	GetIssue(ctx context.Context, issueID string) (tracker.LocalBoardIssue, error)
	CreateIssue(ctx context.Context, title, description string, labels []string) (tracker.LocalBoardIssue, error)
	UpdateIssue(ctx context.Context, issueID string, mutate func(*tracker.LocalBoardIssue) error) (tracker.LocalBoardIssue, error)
	MoveIssue(ctx context.Context, issueID string, state tracker.LocalBoardState) (tracker.LocalBoardIssue, error)
}

type TimelineProvider interface {
	IssueTimeline(ctx context.Context, issueID string) (*timeline.WorkflowTimelineSnapshot, error)
}

type Server struct {
	httpServer       *http.Server
	hub              *hub.Hub[WebEvent]
	webEvents        chan<- WebEvent
	dashboardFS      fs.FS
	listenAddr       string
	snapshotProvider SnapshotProvider
	agentStopper     AgentStopper
	boardProvider    BoardProvider
	detailProvider   tracker.IssueDetailProvider
	timelineProvider TimelineProvider
	mcpTokenMu       sync.Mutex
	mcpTokens        map[string]mcpTokenRecord

	// extraAllowedOrigins holds operator-configured origins from
	// CONTRABASS_ALLOWED_ORIGINS; loopback and same-origin requests are
	// accepted regardless of this list.
	extraAllowedOrigins []string
}

func NewServer(
	addr string,
	provider SnapshotProvider,
	hub *hub.Hub[WebEvent],
	dashboardFS fs.FS,
) *Server {
	listenAddr := normalizeListenAddr(addr)

	return &Server{
		hub:                 hub,
		dashboardFS:         dashboardFS,
		listenAddr:          listenAddr,
		snapshotProvider:    provider,
		mcpTokens:           make(map[string]mcpTokenRecord),
		extraAllowedOrigins: parseAllowedOrigins(os.Getenv(allowedOriginsEnv)),
	}
}

func parseAllowedOrigins(raw string) []string {
	var origins []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		origins = append(origins, strings.TrimSuffix(entry, "/"))
	}
	return origins
}

func normalizeListenAddr(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return defaultListenAddr
	}

	if strings.HasPrefix(trimmed, ":") {
		return "localhost" + trimmed
	}

	return trimmed
}

func (s *Server) SetBoardProvider(provider BoardProvider) {
	s.boardProvider = provider
}

func (s *Server) SetIssueDetailProvider(provider tracker.IssueDetailProvider) {
	s.detailProvider = provider
}

func (s *Server) SetTimelineProvider(provider TimelineProvider) {
	s.timelineProvider = provider
}

func (s *Server) SetAgentStopper(stopper AgentStopper) {
	s.agentStopper = stopper
}

func (s *Server) SetEventSink(sink chan<- WebEvent) {
	s.webEvents = sink
}

func (s *Server) publishEvent(event WebEvent) {
	if s.webEvents == nil {
		return
	}

	select {
	case s.webEvents <- event:
	default:
	}
}

func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}

	return s.Serve(ctx, listener)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if s.snapshotProvider == nil {
		return errors.New("snapshot provider is nil")
	}
	if listener == nil {
		return errors.New("listener is nil")
	}

	mux := s.newMux()
	s.httpServer = &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	errCh := make(chan error, 1)
	go func() {
		serveErr := s.httpServer.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		shutdownErr := s.httpServer.Shutdown(shutdownCtx)
		serveErr := <-errCh
		if shutdownErr != nil {
			return shutdownErr
		}
		return serveErr
	case serveErr := <-errCh:
		return serveErr
	}
}

func (s *Server) newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/state", s.withCORS(s.handleGetState))
	mux.HandleFunc("GET /api/v1/mcp/config", s.withMCPAdminCORS(s.handleGetMCPConfig))
	mux.HandleFunc("OPTIONS /api/v1/mcp/config", s.withMCPAdminCORS(s.handleGetMCPConfig))
	mux.HandleFunc("POST /api/v1/mcp/token", s.withMCPAdminCORS(s.handleCreateMCPToken))
	mux.HandleFunc("OPTIONS /api/v1/mcp/token", s.withMCPAdminCORS(s.handleCreateMCPToken))
	mux.HandleFunc("GET /api/v1/mcp/stream", s.withMCPStreamCORS(s.withMCPTokenAuth(s.handleStreamableHTTP)))
	mux.HandleFunc("POST /api/v1/mcp/stream", s.withMCPStreamCORS(s.withMCPTokenAuth(s.handleStreamableHTTP)))
	mux.HandleFunc("OPTIONS /api/v1/mcp/stream", s.withMCPStreamCORS(s.withMCPTokenAuth(s.handleStreamableHTTP)))
	mux.HandleFunc("GET /api/v1/issues/{issue_id}/details", s.withCORS(s.handleGetIssueDetails))
	mux.HandleFunc("GET /api/v1/issues/{issue_id}/timeline", s.withCORS(s.handleGetIssueTimeline))
	mux.HandleFunc("GET /api/v1/{identifier}", s.withCORS(s.handleGetIssue))
	mux.HandleFunc("GET /api/v1/board/issues", s.withCORS(s.handleListBoardIssues))
	mux.HandleFunc("GET /api/v1/board/issues/{identifier}", s.withCORS(s.handleGetBoardIssue))
	mux.HandleFunc("POST /api/v1/board/issues", s.withCORS(s.handleCreateBoardIssue))
	mux.HandleFunc("PATCH /api/v1/board/issues/{identifier}", s.withCORS(s.handleUpdateBoardIssue))
	mux.HandleFunc("POST /api/v1/running/{issue_id}/stop", s.withCORS(s.handleStopAgent))
	mux.HandleFunc("POST /api/v1/refresh", s.withCORS(s.handleRefresh))
	mux.HandleFunc("GET /api/v1/stream", s.withDashboardStreamCORS(s.handleStreamableHTTP))
	mux.HandleFunc("POST /api/v1/stream", s.withDashboardStreamCORS(s.handleStreamableHTTP))
	mux.HandleFunc("OPTIONS /api/v1/stream", s.withDashboardStreamCORS(s.handleStreamableHTTP))
	mux.HandleFunc("GET /api/v1/events", s.withCORS(s.handleSSE))
	mux.HandleFunc("/api/v1/", s.withCORS(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "not found")
	}))
	if s.dashboardFS != nil {
		mux.Handle("/", SPAHandler(s.dashboardFS))
	}
	return mux
}

// withCORS gates the plain REST/SSE routes. The dashboard is served from this
// same server, so cross-origin access is unnecessary by default: only
// same-origin, loopback, and operator-allowlisted origins pass, and the Host
// check blocks DNS-rebinding requests that reach us under a foreign name.
func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAllowedAPIRequest(r) {
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

func (s *Server) isAllowedAPIRequest(r *http.Request) bool {
	if !s.isTrustedHost(r.Host) {
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients (curl, CLI tooling) and same-origin GET
		// navigations send no Origin header.
		return true
	}
	if isAllowedExactOrigin(r) {
		return true
	}
	if isLoopbackHost(originHost(origin)) {
		return true
	}
	return s.isExtraAllowedOrigin(origin)
}

// isTrustedHost rejects requests whose Host header names neither a loopback
// address, the configured listen host, nor an allowlisted origin's host —
// the remaining DNS-rebinding vector once the Origin check is in place.
func (s *Server) isTrustedHost(host string) bool {
	h := hostWithoutPort(strings.TrimSpace(host))
	if isLoopbackHost(h) {
		return true
	}
	if listenHost := hostWithoutPort(s.listenAddr); listenHost != "" && strings.EqualFold(h, listenHost) {
		return true
	}
	for _, allowed := range s.extraAllowedOrigins {
		if allowed == "*" {
			return true
		}
		if strings.EqualFold(h, originHost(allowed)) {
			return true
		}
	}
	return false
}

func (s *Server) isExtraAllowedOrigin(origin string) bool {
	for _, allowed := range s.extraAllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func originHost(origin string) string {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (s *Server) handleGetState(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.snapshotProvider.Snapshot()
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleGetIssue(w http.ResponseWriter, r *http.Request) {
	identifier := r.PathValue("identifier")
	if strings.TrimSpace(identifier) == "" {
		writeJSONError(w, http.StatusBadRequest, "identifier is required")
		return
	}

	snapshot := s.snapshotProvider.Snapshot()
	issue, ok := snapshot.Issues[identifier]
	if !ok {
		writeJSONError(w, http.StatusNotFound, "issue not found")
		return
	}

	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) handleRefresh(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleStopAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentStopper == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "stop endpoint not configured")
		return
	}

	issueID := strings.TrimSpace(r.PathValue("issue_id"))
	if issueID == "" {
		writeJSONError(w, http.StatusBadRequest, "issue_id is required")
		return
	}

	switch err := s.agentStopper.StopAgent(r.Context(), issueID); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, orchestrator.ErrAgentNotRunning):
		writeJSONError(w, http.StatusNotFound, "agent not running")
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
