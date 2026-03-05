package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
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
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.status, rec.Code)
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))

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

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/refresh", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestNormalizeListenAddr(t *testing.T) {
	assert.Equal(t, defaultListenAddr, normalizeListenAddr(""))
	assert.Equal(t, "localhost:9090", normalizeListenAddr(":9090"))
	assert.Equal(t, "127.0.0.1:9090", normalizeListenAddr("127.0.0.1:9090"))
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
