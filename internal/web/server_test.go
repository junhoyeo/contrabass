package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

type fakeSnapshotProvider struct {
	snapshot orchestrator.StateSnapshot
}

func (f fakeSnapshotProvider) Snapshot() orchestrator.StateSnapshot {
	return f.snapshot
}

func TestServerRoutes(t *testing.T) {
	now := time.Now().UTC()
	provider := fakeSnapshotProvider{
		snapshot: orchestrator.StateSnapshot{
			Stats: orchestrator.Stats{
				Running:        1,
				MaxAgents:      4,
				TotalTokensIn:  100,
				TotalTokensOut: 200,
				StartTime:      now.Add(-time.Minute),
				PollCount:      2,
			},
			Running: []orchestrator.RunningEntry{{IssueID: "issue-1"}},
			Backoff: []types.BackoffEntry{{IssueID: "issue-1", Attempt: 2, RetryAt: now}},
			Issues: map[string]types.Issue{
				"issue-1": {
					ID:    "issue-1",
					Title: "Issue One",
				},
			},
			GeneratedAt: now,
		},
	}

	s := &Server{snapshotProvider: provider, dashboardFS: nil}
	h := s.newMux()

	tests := []struct {
		name         string
		method       string
		target       string
		status       int
		wantErr      string
		wantIssueID  string
		wantJSONKeys []string
	}{
		{
			name:         "get_state_returns_snapshot_json",
			method:       http.MethodGet,
			target:       "/api/v1/state",
			status:       http.StatusOK,
			wantJSONKeys: []string{"stats", "running", "backoff", "issues", "generated_at"},
		},
		{
			name:        "get_issue_returns_issue_detail",
			method:      http.MethodGet,
			target:      "/api/v1/issue-1",
			status:      http.StatusOK,
			wantIssueID: "issue-1",
		},
		{
			name:    "get_issue_returns_404_for_unknown_identifier",
			method:  http.MethodGet,
			target:  "/api/v1/unknown",
			status:  http.StatusNotFound,
			wantErr: "issue not found",
		},
		{
			name:   "post_refresh_returns_accepted",
			method: http.MethodPost,
			target: "/api/v1/refresh",
			status: http.StatusAccepted,
		},
		{
			name:         "post_stream_ping_returns_json_rpc",
			method:       http.MethodPost,
			target:       "/api/v1/stream",
			status:       http.StatusOK,
			wantJSONKeys: []string{"jsonrpc", "id", "result"},
		},
		{
			name:    "unknown_api_path_returns_json_404",
			method:  http.MethodGet,
			target:  "/api/v1/does-not-exist/path",
			status:  http.StatusNotFound,
			wantErr: "not found",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := newLocalWebRequest(tt.method, tt.target, nil)
			if tt.target == "/api/v1/stream" {
				req = newLocalWebRequest(tt.method, tt.target, strings.NewReader(`{"jsonrpc":"2.0","id":"ping-1","method":"dashboard.ping"}`))
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.status, rec.Code)
			// No Origin header on the request means no CORS grant is echoed.
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

			if tt.status != http.StatusAccepted {
				assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			}

			if len(tt.wantJSONKeys) > 0 {
				var payload map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
				for _, key := range tt.wantJSONKeys {
					_, ok := payload[key]
					assert.True(t, ok, "missing key %q", key)
				}
			}

			if tt.wantIssueID != "" {
				var issue map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))
				assert.Equal(t, tt.wantIssueID, issue["id"])
			}

			if tt.wantErr != "" {
				var errResp map[string]string
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
				assert.Equal(t, tt.wantErr, errResp["error"])
			}
		})
	}
}

func TestServerCORSPreflight(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	s := &Server{snapshotProvider: provider, dashboardFS: nil}
	h := s.newMux()

	req := newLocalWebRequest(http.MethodOptions, "/api/v1/refresh", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	// The validated origin is echoed back; the wildcard grant is gone.
	assert.Equal(t, "http://localhost:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))
	assert.Equal(t, "GET, POST, PATCH, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Accept, Authorization, Content-Type, Mcp-Protocol-Version", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestWithCORSOriginPolicy(t *testing.T) {
	tests := []struct {
		name                string
		method              string
		target              string
		host                string
		origin              string
		extraAllowedOrigins []string
		wantStatus          int
		wantAllowOrigin     string
	}{
		{
			name:       "no_origin_curl_style_request_allowed",
			method:     http.MethodGet,
			target:     "/api/v1/state",
			wantStatus: http.StatusOK,
		},
		{
			name:            "same_origin_mutation_allowed",
			method:          http.MethodPost,
			target:          "/api/v1/refresh",
			origin:          "http://localhost:8080",
			wantStatus:      http.StatusAccepted,
			wantAllowOrigin: "http://localhost:8080",
		},
		{
			name:            "loopback_origin_on_other_port_allowed",
			method:          http.MethodGet,
			target:          "/api/v1/state",
			origin:          "http://127.0.0.1:5173",
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "http://127.0.0.1:5173",
		},
		{
			name:       "cross_origin_mutation_rejected",
			method:     http.MethodPost,
			target:     "/api/v1/refresh",
			origin:     "https://evil.example",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "cross_origin_sse_rejected",
			method:     http.MethodGet,
			target:     "/api/v1/events",
			origin:     "https://evil.example",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "cross_origin_state_read_rejected",
			method:     http.MethodGet,
			target:     "/api/v1/state",
			origin:     "https://evil.example",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "foreign_host_without_origin_rejected_dns_rebinding",
			method:     http.MethodGet,
			target:     "/api/v1/state",
			host:       "evil.example:8080",
			wantStatus: http.StatusForbidden,
		},
		{
			name:                "allowlisted_origin_allowed",
			method:              http.MethodPost,
			target:              "/api/v1/refresh",
			origin:              "https://dash.example.com",
			extraAllowedOrigins: []string{"https://dash.example.com"},
			wantStatus:          http.StatusAccepted,
			wantAllowOrigin:     "https://dash.example.com",
		},
		{
			name:                "allowlisted_host_trusted_without_origin",
			method:              http.MethodGet,
			target:              "/api/v1/state",
			host:                "dash.example.com:8080",
			extraAllowedOrigins: []string{"https://dash.example.com"},
			wantStatus:          http.StatusOK,
		},
		{
			name:                "wildcard_escape_hatch_restores_allow_all",
			method:              http.MethodPost,
			target:              "/api/v1/refresh",
			host:                "anything.example:8080",
			origin:              "https://evil.example",
			extraAllowedOrigins: []string{"*"},
			wantStatus:          http.StatusAccepted,
			wantAllowOrigin:     "https://evil.example",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
			s := &Server{snapshotProvider: provider, dashboardFS: nil, extraAllowedOrigins: tt.extraAllowedOrigins}
			h := s.newMux()

			req := newLocalWebRequest(tt.method, tt.target, nil)
			if tt.host != "" {
				req.Host = tt.host
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantAllowOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
			if tt.wantStatus == http.StatusForbidden {
				assert.Equal(t, "origin not allowed", readErrorMessage(t, rec))
			}
		})
	}
}

func TestWithCORSCrossOriginBoardMutationRejected(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}
	s := &Server{snapshotProvider: fakeSnapshotProvider{}, boardProvider: bp}
	h := s.newMux()

	req := newLocalWebRequest(http.MethodPost, "/api/v1/board/issues", strings.NewReader(`{"title":"pwn"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, bp.issues, "cross-origin request must not reach the board provider")
}

func TestNewServerReadsAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv(allowedOriginsEnv, " https://dash.example.com/ , https://other.example ,, ")

	s := NewServer("", fakeSnapshotProvider{}, nil, nil)

	assert.Equal(t, []string{"https://dash.example.com", "https://other.example"}, s.extraAllowedOrigins)
}

func TestNormalizeListenAddr(t *testing.T) {
	assert.Equal(t, defaultListenAddr, normalizeListenAddr(""))
	assert.Equal(t, "localhost:9090", normalizeListenAddr(":9090"))
	assert.Equal(t, "127.0.0.1:9090", normalizeListenAddr("127.0.0.1:9090"))
}

func TestPublishEvent(t *testing.T) {
	tests := []struct {
		name        string
		setupSink   func(*Server) chan WebEvent
		invokeAsync bool
		assertion   func(*testing.T, chan WebEvent, WebEvent)
	}{
		{
			name: "sends event when sink is configured",
			setupSink: func(s *Server) chan WebEvent {
				sink := make(chan WebEvent, 1)
				s.SetEventSink(sink)
				return sink
			},
			assertion: func(t *testing.T, sink chan WebEvent, expected WebEvent) {
				t.Helper()
				select {
				case got := <-sink:
					assert.Equal(t, expected, got)
				default:
					t.Fatal("expected event to be sent")
				}
			},
		},
		{
			name: "does not block when sink is full",
			setupSink: func(s *Server) chan WebEvent {
				sink := make(chan WebEvent, 1)
				sink <- WebEvent{Type: "already-buffered"}
				s.SetEventSink(sink)
				return sink
			},
			invokeAsync: true,
			assertion: func(t *testing.T, sink chan WebEvent, _ WebEvent) {
				t.Helper()
				assert.Equal(t, 1, len(sink))
				assert.Equal(t, "already-buffered", (<-sink).Type)
			},
		},
		{
			name: "no-op when sink is nil",
			setupSink: func(_ *Server) chan WebEvent {
				return nil
			},
			assertion: func(t *testing.T, _ chan WebEvent, _ WebEvent) {
				t.Helper()
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			sink := tt.setupSink(s)
			event := WebEvent{Type: "board_issue_updated", Timestamp: time.Now().UTC()}

			if tt.invokeAsync {
				done := make(chan struct{})
				go func() {
					s.publishEvent(event)
					close(done)
				}()
				select {
				case <-done:
				case <-time.After(100 * time.Millisecond):
					t.Fatal("publishEvent blocked on full sink")
				}
			} else {
				s.publishEvent(event)
			}

			tt.assertion(t, sink, event)
		})
	}
}

type fakeAgentStopper struct {
	calls []string
	err   error
}

func (f *fakeAgentStopper) StopAgent(_ context.Context, issueID string) error {
	f.calls = append(f.calls, issueID)
	return f.err
}

func TestHandleStopAgent(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}

	tests := []struct {
		name        string
		stopper     AgentStopper
		issueID     string
		wantStatus  int
		wantErrBody string
	}{
		{
			name:       "running entry returns 202",
			stopper:    &fakeAgentStopper{},
			issueID:    "ISS-A",
			wantStatus: http.StatusAccepted,
		},
		{
			name:        "missing entry returns 404",
			stopper:     &fakeAgentStopper{err: orchestrator.ErrAgentNotRunning},
			issueID:     "ISS-MISSING",
			wantStatus:  http.StatusNotFound,
			wantErrBody: "agent not running",
		},
		{
			name:        "stopper internal error returns 500",
			stopper:     &fakeAgentStopper{err: errors.New("kaboom")},
			issueID:     "ISS-A",
			wantStatus:  http.StatusInternalServerError,
			wantErrBody: "kaboom",
		},
		{
			name:        "no stopper configured returns 503",
			stopper:     nil,
			issueID:     "ISS-A",
			wantStatus:  http.StatusServiceUnavailable,
			wantErrBody: "stop endpoint not configured",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{snapshotProvider: provider, dashboardFS: nil, agentStopper: tt.stopper}
			h := s.newMux()

			target := fmt.Sprintf("/api/v1/running/%s/stop", tt.issueID)
			req := newLocalWebRequest(http.MethodPost, target, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantErrBody != "" {
				var body map[string]string
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Contains(t, body["error"], tt.wantErrBody)
			}
		})
	}
}

func TestHandleStopAgent_RejectsBlankIssueID(t *testing.T) {
	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	stopper := &fakeAgentStopper{}
	s := &Server{snapshotProvider: provider, dashboardFS: nil, agentStopper: stopper}
	h := s.newMux()

	// PathValue extracts the issue_id; an explicit blank segment 404s at the
	// router. Use a whitespace-only segment to drive the BadRequest branch.
	req := newLocalWebRequest(http.MethodPost, "/api/v1/running/%20/stop", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, stopper.calls, "blank issue_id must not invoke the stopper")
}

func TestServeSetsReadHeaderTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	server := &Server{snapshotProvider: provider, dashboardFS: nil}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, server.Serve(ctx, listener))

	assert.Equal(t, readHeaderTimeout, server.httpServer.ReadHeaderTimeout)
}

func TestStartReturnsErrorWhenPortAlreadyInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	server := &Server{
		listenAddr:       listener.Addr().String(),
		snapshotProvider: provider,
		dashboardFS:      nil,
	}

	err = server.Start(context.Background())
	require.Error(t, err)
}

func TestStartShutsDownOnContextCancel(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	provider := fakeSnapshotProvider{snapshot: orchestrator.StateSnapshot{Issues: map[string]types.Issue{}}}
	server := &Server{
		listenAddr:       addr,
		snapshotProvider: provider,
		dashboardFS:      nil,
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	require.Eventually(t, func() bool {
		resp, reqErr := http.Get(fmt.Sprintf("http://%s/api/v1/state", addr))
		if reqErr != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	cancel()

	select {
	case startErr := <-errCh:
		require.NoError(t, startErr)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for server shutdown")
	}
}
