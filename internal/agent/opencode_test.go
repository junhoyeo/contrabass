package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeRunner_CompileTimeCheck(t *testing.T) {
	var _ AgentRunner = (*OpenCodeRunner)(nil)
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second)
	require.NotNil(t, runner)
	assert.Equal(t, 30*time.Second, runner.httpClient.Timeout)
	assert.Equal(t, time.Duration(0), runner.streamClient.Timeout)
}

func TestOpenCodeRunner_Close(t *testing.T) {
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second)
	require.NoError(t, runner.Close())
}

func TestOpenCodeRunner_Start(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"busy"}}}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"hello"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-1", Title: "OpenCode"}, workspace, "hello")
	require.NoError(t, err)
	require.Equal(t, "sess-1", proc.SessionID)
	require.Equal(t, 4242, proc.PID)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 3, 3*time.Second)
	require.Len(t, events, 3)
	assert.Equal(t, "session.status", events[0].Type)
	assert.Equal(t, "message.part.updated", events[1].Type)
	assert.Equal(t, "session.status", events[2].Type)
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_StartWithAuth(t *testing.T) {
	var sessionAuth, promptAuth, eventAuth atomic.Bool
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		isAuthed := ok && user == "alice" && pass == "secret"

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			sessionAuth.Store(isAuthed)
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			promptAuth.Store(isAuthed)
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			eventAuth.Store(isAuthed)
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := NewOpenCodeRunner("opencode serve", 0, "secret", "alice", 2*time.Second)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	require.NoError(t, err)
	assertDoneNil(t, proc.Done)

	assert.True(t, sessionAuth.Load())
	assert.True(t, promptAuth.Load())
	assert.True(t, eventAuth.Load())
}

func TestOpenCodeRunner_Stop(t *testing.T) {
	abortCalled := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/abort":
			abortCalled <- r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner := newTestOpenCodeRunner(server.URL)
	err := runner.Stop(&AgentProcess{SessionID: "sess-1"})
	require.NoError(t, err)

	select {
	case path := <-abortCalled:
		assert.Equal(t, "/session/sess-1/abort", path)
	case <-time.After(time.Second):
		t.Fatal("expected abort endpoint to be called")
	}
}

func TestOpenCodeRunner_SessionError(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.error","properties":{"sessionID":"sess-1"},"error":"boom"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	require.NoError(t, err)

	select {
	case doneErr := <-proc.Done:
		require.Error(t, doneErr)
		assert.Contains(t, doneErr.Error(), "session error")
	case <-time.After(3 * time.Second):
		t.Fatal("expected session error to terminate process")
	}
}

func TestOpenCodeRunner_SSEParsing(t *testing.T) {
	raw := strings.Join([]string{
		": this is a comment",
		"id: 42",
		"event: custom",
		"data: hello",
		"data: world",
		"retry: 1000",
		"",
		"data: single line",
		"",
		"data: spaced value",
		"",
	}, "\n")

	reader := newSSEReader(strings.NewReader(raw))

	event1, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "42", event1.ID)
	assert.Equal(t, "custom", event1.Event)
	assert.Equal(t, "hello\nworld", event1.Data)

	event2, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "42", event2.ID)
	assert.Equal(t, "message", event2.Event)
	assert.Equal(t, "single line", event2.Data)

	event3, err := reader.Next()
	require.NoError(t, err)
	assert.Equal(t, "42", event3.ID)
	assert.Equal(t, "message", event3.Event)
	assert.Equal(t, "spaced value", event3.Data)

	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestOpenCodeRunner_SSESessionFilter(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-2"},"content":"other"}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"mine"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	require.NoError(t, err)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 2, 3*time.Second)
	require.Len(t, events, 2)
	assert.Equal(t, "mine", events[0].Data["content"])
	assert.Equal(t, "session.status", events[1].Type)
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_HeartbeatIgnored(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"server.heartbeat","properties":{"sessionID":"sess-1"}}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	require.NoError(t, err)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 1, 3*time.Second)
	require.Len(t, events, 1)
	assert.Equal(t, "session.status", events[0].Type)
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_ConcurrentSessions(t *testing.T) {
	var sessionCounter int32
	var prompts sync.Map
	var aborts sync.Map

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			id := fmt.Sprintf("sess-%d", atomic.AddInt32(&sessionCounter, 1))
			_, _ = io.WriteString(w, mustJSON(map[string]interface{}{"id": id}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			parts := strings.Split(r.URL.Path, "/")
			require.Len(t, parts, 4)
			sessionID := parts[2]
			prompts.Store(sessionID, true)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
			parts := strings.Split(r.URL.Path, "/")
			require.Len(t, parts, 4)
			sessionID := parts[2]
			aborts.Store(sessionID, true)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			for !hasSession(&prompts, "sess-1") || !hasSession(&prompts, "sess-2") {
				time.Sleep(10 * time.Millisecond)
			}
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"a"}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-2"},"content":"b"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-2","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc1, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello-1")
	require.NoError(t, err)
	proc2, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello-2")
	require.NoError(t, err)

	require.Equal(t, 4242, proc1.PID)
	require.Equal(t, 4242, proc2.PID)

	events1 := collectOpenCodeEvents(t, proc1.Events, proc1.Done, 2, 4*time.Second)
	events2 := collectOpenCodeEvents(t, proc2.Events, proc2.Done, 2, 4*time.Second)

	assert.Equal(t, "a", events1[0].Data["content"])
	assert.Equal(t, "b", events2[0].Data["content"])
	assertDoneNil(t, proc1.Done)
	assertDoneNil(t, proc2.Done)

	require.NoError(t, runner.Stop(proc1))
	require.NoError(t, runner.Stop(proc2))

	assert.Eventually(t, func() bool {
		return hasSession(&aborts, "sess-1") && hasSession(&aborts, "sess-2")
	}, time.Second, 20*time.Millisecond)
}

func TestOpenCodeRunner_DefaultWorkDir(t *testing.T) {
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second)
	runner.SetWorkDir("/tmp/existing")

	assert.Equal(t, "/tmp/existing", runner.defaultWorkDir())
}

func TestOpenCodeRunner_SetExtraEnvCopiesInput(t *testing.T) {
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second)
	env := []string{"OPENCODE_CONFIG=/tmp/opencode.json"}

	runner.SetExtraEnv(env)
	env[0] = "OPENCODE_CONFIG=/tmp/mutated.json"

	assert.Equal(t, []string{"OPENCODE_CONFIG=/tmp/opencode.json"}, runner.extraEnv)
}

func TestOpenCodeRunner_PortForNewServerLocked(t *testing.T) {
	tests := []struct {
		name    string
		base    int
		servers map[string]*openCodeServer
		want    int
	}{
		{
			name: "disabled when base port unset",
			base: 0,
			want: 0,
		},
		{
			name: "uses configured base port first",
			base: 8787,
			want: 8787,
		},
		{
			name: "increments past ports already assigned to other workspaces",
			base: 8787,
			servers: map[string]*openCodeServer{
				"workspace-a": {port: 8787},
				"workspace-b": {port: 8788},
			},
			want: 8789,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewOpenCodeRunner("opencode serve", tt.base, "", "", time.Second)
			runner.servers = tt.servers

			runner.mu.Lock()
			port := runner.portForNewServerLocked()
			runner.mu.Unlock()

			assert.Equal(t, tt.want, port)
		})
	}
}

func TestOpenCodeRunner_WorkspaceScopedServers(t *testing.T) {
	startA := make(chan struct{})
	startB := make(chan struct{})

	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-a"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-a/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startA)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-a/abort":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startA
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-a"},"content":"workspace-a"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-a","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-b"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-b/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startB)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-b/abort":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startB
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-b"},"content":"workspace-b"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-b","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverB.Close()

	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", 2*time.Second)
	primeTestOpenCodeServer(runner, workspaceA, serverA.URL, 4242)
	primeTestOpenCodeServer(runner, workspaceB, serverB.URL, 4343)

	procA, err := runner.Start(context.Background(), types.Issue{}, workspaceA, "hello-a")
	require.NoError(t, err)
	procB, err := runner.Start(context.Background(), types.Issue{}, workspaceB, "hello-b")
	require.NoError(t, err)

	require.Equal(t, 4242, procA.PID)
	require.Equal(t, 4343, procB.PID)

	eventsA := collectOpenCodeEvents(t, procA.Events, procA.Done, 2, 3*time.Second)
	eventsB := collectOpenCodeEvents(t, procB.Events, procB.Done, 2, 3*time.Second)

	assert.Equal(t, "workspace-a", eventsA[0].Data["content"])
	assert.Equal(t, "workspace-b", eventsB[0].Data["content"])
	assertDoneNil(t, procA.Done)
	assertDoneNil(t, procB.Done)

	require.NoError(t, runner.Stop(procA))
	require.NoError(t, runner.Stop(procB))
}

func TestOpenCodeRunner_EnsureServerStartsWorkspacesInParallel(t *testing.T) {
	stateDir := t.TempDir()
	binaryPath := writeTestOpenCodeServerBinary(t, stateDir)

	runner := NewOpenCodeRunner(binaryPath, 8787, "", "", 2*time.Second)
	runner.SetExtraEnv([]string{"TEST_STATE_DIR=" + stateDir})
	t.Cleanup(func() {
		require.NoError(t, runner.Close())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workspaceA := filepath.Join(stateDir, "workspace-a")
	workspaceB := filepath.Join(stateDir, "workspace-b")
	require.NoError(t, os.MkdirAll(workspaceA, 0o755))
	require.NoError(t, os.MkdirAll(workspaceB, 0o755))

	type result struct {
		url string
		pid int
		err error
	}

	results := make(chan result, 2)
	go func() {
		url, pid, err := runner.ensureServer(ctx, workspaceA)
		results <- result{url: url, pid: pid, err: err}
	}()
	go func() {
		url, pid, err := runner.ensureServer(ctx, workspaceB)
		results <- result{url: url, pid: pid, err: err}
	}()

	assert.Eventually(t, func() bool {
		_, errA := os.Stat(filepath.Join(stateDir, "started-8787"))
		_, errB := os.Stat(filepath.Join(stateDir, "started-8788"))
		return errA == nil && errB == nil
	}, time.Second, 20*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "release"), []byte("ok"), 0o644))

	res1 := <-results
	res2 := <-results
	require.NoError(t, res1.err)
	require.NoError(t, res2.err)
	assert.NotZero(t, res1.pid)
	assert.NotZero(t, res2.pid)
	assert.ElementsMatch(t, []string{
		"http://127.0.0.1:8787",
		"http://127.0.0.1:8788",
	}, []string{res1.url, res2.url})
}

func newTestOpenCodeRunner(serverURL string) *OpenCodeRunner {
	r := NewOpenCodeRunner("opencode serve", 0, "", "", 2*time.Second)
	primeTestOpenCodeServer(r, "", serverURL, 4242)
	return r
}

func primeTestOpenCodeServer(r *OpenCodeRunner, workspace, serverURL string, pid int) {
	r.servers[serverKey(workspace)] = &openCodeServer{
		url:     serverURL,
		process: &exec.Cmd{Process: &os.Process{Pid: pid}},
		port:    0,
	}
}

func writeTestOpenCodeServerBinary(t *testing.T, stateDir string) string {
	t.Helper()

	scriptPath := filepath.Join(stateDir, "fake-opencode.sh")
	script := `#!/bin/sh
set -eu

state_dir="${TEST_STATE_DIR:?TEST_STATE_DIR is required}"
port="0"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --port)
      port="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

touch "$state_dir/started-$port"
while [ ! -f "$state_dir/release" ]; do
  sleep 0.05
done

echo "listening on http://127.0.0.1:$port"
sleep 30
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	return scriptPath
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSEEvent(w http.ResponseWriter, eventType, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\n", eventType)
	for _, line := range strings.Split(data, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func collectOpenCodeEvents(t *testing.T, events <-chan types.AgentEvent, done <-chan error, expected int, timeout time.Duration) []types.AgentEvent {
	t.Helper()
	_ = done
	out := make([]types.AgentEvent, 0, expected)
	deadline := time.After(timeout)

	for len(out) < expected {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out collecting events: got %d", len(out))
		}
	}

	return out
}

func assertDoneNil(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("expected done channel to be signaled")
	}
}

func mustJSON(v map[string]interface{}) string {
	bytes, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

func hasSession(m *sync.Map, sessionID string) bool {
	_, ok := m.Load(sessionID)
	return ok
}
