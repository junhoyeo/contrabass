package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/hub"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
)

const defaultListenAddr = "localhost:8080"

type SnapshotProvider interface {
	Snapshot() orchestrator.StateSnapshot
}

type Server struct {
	httpServer       *http.Server
	hub              *hub.Hub[orchestrator.OrchestratorEvent]
	dashboardFS      fs.FS
	listenAddr       string
	snapshotProvider SnapshotProvider
}

func NewServer(
	addr string,
	provider SnapshotProvider,
	hub *hub.Hub[orchestrator.OrchestratorEvent],
	dashboardFS fs.FS,
) *Server {
	listenAddr := normalizeListenAddr(addr)

	return &Server{
		hub:              hub,
		dashboardFS:      dashboardFS,
		listenAddr:       listenAddr,
		snapshotProvider: provider,
	}
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
	s.httpServer = &http.Server{Handler: mux}

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
	mux.HandleFunc("GET /api/v1/{identifier}", s.withCORS(s.handleGetIssue))
	mux.HandleFunc("POST /api/v1/refresh", s.withCORS(s.handleRefresh))
	mux.HandleFunc("GET /api/v1/events", s.withCORS(s.handleSSE))
	mux.HandleFunc("/api/v1/", s.withCORS(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "not found")
	}))
	if s.dashboardFS != nil {
		mux.Handle("/", SPAHandler(s.dashboardFS))
	}
	return mux
}

func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
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

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
