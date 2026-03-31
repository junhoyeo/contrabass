package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOMXRunner_CompileTimeCheck(t *testing.T) {
	var _ AgentRunner = (*OMXRunner)(nil)
}

func TestOMXRunner_Defaults(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner := NewOMXRunner(cfg, 0)
	require.NotNil(t, runner)
	assert.Equal(t, "omx", runner.binaryPath)
	assert.Equal(t, "1:executor", runner.teamSpec)
	assert.Equal(t, 1000*time.Millisecond, runner.pollInterval)
	assert.Equal(t, 15000*time.Millisecond, runner.startupTimeout)
}

func TestOMXRunner_StartStopLifecycle(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "omx-commands.log")
	server := newFakeTeamCLIServer(t, logPath)
	defer server.Close()

	cfg := &config.WorkflowConfig{
		OMX: config.OMXConfig{
			BinaryPath: server.binaryPath,
			TeamSpec:   "1:executor",
		},
	}
	runner := NewOMXRunner(cfg, time.Second)

	issue := types.Issue{ID: "CB-101", Title: "Add OMX runner"}
	proc, err := runner.Start(context.Background(), issue, workspace, "Implement the feature")
	require.NoError(t, err)
	require.NotEmpty(t, proc.SessionID)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 4, 5*time.Second)
	require.Len(t, events, 4)
	assert.Equal(t, "turn/started", events[0].Type)
	assert.Equal(t, "session.status", events[1].Type)
	assert.Equal(t, "item/completed", events[2].Type)
	assert.Equal(t, "task/completed", events[3].Type)
	assertDoneNil(t, proc.Done)

	promptData, err := os.ReadFile(proc.serverURL)
	require.NoError(t, err)
	assert.Contains(t, string(promptData), "Add OMX runner")
	assert.Contains(t, string(promptData), "Implement the feature")

	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	logged := string(logData)
	assert.Contains(t, logged, "team 1:executor")
	assert.Contains(t, logged, "team api get-summary")
	assert.Contains(t, logged, "team api list-tasks")
	assert.Contains(t, logged, "team shutdown "+proc.SessionID+" --force")
}

func TestOMXRunner_RalphShutdown(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "omx-ralph.log")
	server := newFakeTeamCLIServer(t, logPath)
	defer server.Close()

	cfg := &config.WorkflowConfig{
		OMX: config.OMXConfig{
			BinaryPath: server.binaryPath,
			TeamSpec:   "1:executor",
			Ralph:      true,
		},
	}
	runner := NewOMXRunner(cfg, time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-102", Title: "Ralph mode"}, workspace, "Handle shutdown")
	require.NoError(t, err)
	require.NoError(t, runner.Stop(proc))

	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logData), "team ralph 1:executor")
	assert.Contains(t, string(logData), "team shutdown "+proc.SessionID+" --force --ralph")
}

func TestOMXRunner_FailedTask(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "omx-failed.log")
	server := newFakeTeamCLIServer(t, logPath)
	server.fail = true
	defer server.Close()

	cfg := &config.WorkflowConfig{OMX: config.OMXConfig{BinaryPath: server.binaryPath}}
	runner := NewOMXRunner(cfg, time.Second)
	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-103", Title: "Failing task"}, workspace, "Make it fail")
	require.NoError(t, err)

	event1 := waitForEvent(t, proc.Events)
	event2 := waitForEvent(t, proc.Events)
	event3 := waitForEvent(t, proc.Events)
	assert.Equal(t, "turn/started", event1.Type)
	assert.Equal(t, "session.status", event2.Type)
	assert.Equal(t, "turn/failed", event3.Type)

	select {
	case doneErr := <-proc.Done:
		require.Error(t, doneErr)
		assert.Contains(t, doneErr.Error(), "simulated failure")
	case <-time.After(3 * time.Second):
		t.Fatal("expected done error")
	}
}

type fakeTeamCLIServer struct {
	httpServer *httptest.Server
	binaryPath string
	logPath    string
	fail       bool

	mu    sync.Mutex
	teams map[string]int
}

func newFakeTeamCLIServer(t *testing.T, logPath string) *fakeTeamCLIServer {
	t.Helper()

	state := &fakeTeamCLIServer{logPath: logPath, teams: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			var body struct {
				Args []string `json:"args"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			teamName := parseFakeTeamName(body.Args)
			state.mu.Lock()
			state.teams[teamName] = 0
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("started"))
		case "/api":
			var body struct {
				Args []string `json:"args"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			op, teamName := parseFakeTeamAPI(body.Args)
			state.mu.Lock()
			count := state.teams[teamName]
			state.teams[teamName] = count + 1
			state.mu.Unlock()

			switch op {
			case "get-summary":
				status := "pending"
				if count >= 1 {
					if state.fail {
						status = "failed"
					} else {
						status = "completed"
					}
				} else if count == 0 {
					status = "in_progress"
				}
				resp := map[string]interface{}{
					"ok":        true,
					"operation": op,
					"data": map[string]interface{}{
						"summary": map[string]interface{}{
							"teamName":    teamName,
							"workerCount": 1,
							"tasks": map[string]interface{}{
								"total":       1,
								"pending":     map[bool]int{true: 1, false: 0}[status == "pending"],
								"blocked":     0,
								"in_progress": map[bool]int{true: 1, false: 0}[status == "in_progress"],
								"completed":   map[bool]int{true: 1, false: 0}[status == "completed"],
								"failed":      map[bool]int{true: 1, false: 0}[status == "failed"],
							},
							"workers":             []map[string]interface{}{{"name": "worker-1", "alive": true, "lastTurnAt": time.Now().UTC().Format(time.RFC3339), "turnsWithoutProgress": 0}},
							"nonReportingWorkers": []string{},
						},
					},
				}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "list-tasks":
				status := "in_progress"
				if count >= 1 {
					if state.fail {
						status = "failed"
					} else {
						status = "completed"
					}
				}
				task := map[string]interface{}{
					"id":          "1",
					"subject":     "Worker 1",
					"description": "Do work",
					"status":      status,
				}
				if status == "completed" {
					task["result"] = "implemented successfully"
				}
				if status == "failed" {
					task["error"] = "simulated failure"
				}
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{"tasks": []interface{}{task}}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "send-message":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"message": map[string]interface{}{
						"message_id":  "msg-001",
						"from_worker": "coordinator",
						"to_worker":   "worker-1",
						"body":        "test",
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "broadcast":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"count":    1,
					"messages": []interface{}{},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "mailbox-list":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"worker":   "worker-1",
					"count":    0,
					"messages": []interface{}{},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "mailbox-mark-delivered", "mailbox-mark-notified":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": json.RawMessage("{}")}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "write-shutdown-request":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": json.RawMessage("{}")}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-shutdown-ack":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"worker": "worker-1",
					"ack":    map[string]interface{}{"status": "accept", "reason": "restart"},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-stall-state":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"team_name":          teamName,
					"team_stalled":       false,
					"leader_stale":       false,
					"stalled_workers":    []string{},
					"dead_workers":       []string{},
					"pending_task_count": 0,
					"all_workers_idle":   false,
					"idle_workers":       []string{},
					"reasons":            []string{},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-idle-state":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"team_name":         teamName,
					"worker_count":      1,
					"idle_worker_count": 0,
					"idle_workers":      []string{},
					"non_idle_workers":  []string{"worker-1"},
					"all_workers_idle":  false,
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-events":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"count":  1,
					"cursor": "evt-read-1",
					"events": []interface{}{map[string]interface{}{
						"event_id":   "evt-read-1",
						"team":       teamName,
						"type":       "worker_state_changed",
						"worker":     "worker-1",
						"state":      "active",
						"prev_state": "idle",
						"created_at": time.Now().UTC().Format(time.RFC3339),
					}},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "await-event":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"status": "timeout",
					"cursor": "",
					"event":  nil,
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "append-event":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"event": map[string]interface{}{
						"event_id":   "evt-001",
						"team":       teamName,
						"type":       "worker_state_changed",
						"worker":     "worker-1",
						"created_at": time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "create-task":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"task": map[string]interface{}{
						"id":          "new-task-1",
						"subject":     "created task",
						"description": "test",
						"status":      "pending",
						"version":     1,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-task":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"task": map[string]interface{}{
						"id":          "1",
						"subject":     "Worker 1",
						"description": "Do work",
						"status":      "in_progress",
						"version":     2,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "update-task":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"task": map[string]interface{}{
						"id":          "1",
						"subject":     "updated task",
						"description": "test",
						"status":      "pending",
						"version":     2,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "claim-task":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"ok":         true,
					"claimToken": "token-001",
					"task": map[string]interface{}{
						"id":          "1",
						"subject":     "claimed task",
						"description": "test",
						"status":      "in_progress",
						"version":     2,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
						"claim": map[string]interface{}{
							"owner":        "worker-1",
							"token":        "token-001",
							"leased_until": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
						},
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "transition-task-status":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"ok": true,
					"task": map[string]interface{}{
						"id":          "1",
						"subject":     "transitioned task",
						"description": "test",
						"status":      "in_progress",
						"version":     3,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "release-task-claim":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"ok": true,
					"task": map[string]interface{}{
						"id":          "1",
						"subject":     "released task",
						"description": "test",
						"status":      "pending",
						"version":     4,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-worker-heartbeat":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"worker": "worker-1",
					"heartbeat": map[string]interface{}{
						"pid":          1234,
						"last_turn_at": time.Now().UTC().Format(time.RFC3339),
						"turn_count":   5,
						"alive":        true,
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			case "read-worker-status":
				resp := map[string]interface{}{"ok": true, "operation": op, "data": map[string]interface{}{
					"worker": "worker-1",
					"status": map[string]interface{}{
						"state":              "idle",
						"consecutive_errors": 0,
						"updated_at":         time.Now().UTC().Format(time.RFC3339),
					},
				}}
				require.NoError(t, json.NewEncoder(w).Encode(resp))
			default:
				http.Error(w, "unknown op", http.StatusBadRequest)
			}
		case "/shutdown":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("shutdown"))
		default:
			http.NotFound(w, r)
		}
	}))
	state.httpServer = server

	scriptPath := filepath.Join(t.TempDir(), "fake-omx.py")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import json, os, sys, urllib.request
base = %q
log_path = %q
args = sys.argv[1:]
with open(log_path, 'a', encoding='utf-8') as fh:
    fh.write(' '.join(args) + '\n')

def post(path, payload):
    data = json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(base + path, data=data, headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req) as resp:
        return resp.read().decode('utf-8')

if len(args) >= 2 and args[0] == 'team' and args[1] == 'api':
    print(post('/api', {'args': args}))
    sys.exit(0)
elif len(args) >= 2 and args[0] == 'team' and args[1] == 'shutdown':
    post('/shutdown', {'args': args})
    print('Team shutdown complete')
    sys.exit(0)
elif len(args) >= 1 and args[0] == 'team':
    post('/start', {'args': args})
    print('Team started')
    sys.exit(0)
print('unsupported', file=sys.stderr)
sys.exit(1)
`, server.URL, logPath)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	state.binaryPath = scriptPath
	return state
}

func (s *fakeTeamCLIServer) Close() {
	s.httpServer.Close()
}

func parseFakeTeamName(args []string) string {
	for i, arg := range args {
		if i == 0 || arg == "ralph" || strings.Contains(arg, ":") {
			continue
		}
		return slugifyTeamName(arg)
	}
	return "team-task"
}

func parseFakeTeamAPI(args []string) (string, string) {
	if len(args) < 5 {
		return "", ""
	}
	op := args[2]
	input := args[4]
	var payload map[string]string
	_ = json.Unmarshal([]byte(input), &payload)
	return op, payload["team_name"]
}
