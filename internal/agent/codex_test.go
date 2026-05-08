package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexRunner_PolicyDefaults_OnWire(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "capture-wire"), 15*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	event := waitForEventType(t, proc.Events, "helper/requests")
	threadParams := eventDataMap(t, event, "threadStart")
	turnParams := eventDataMap(t, event, "turnStart")
	wantSandbox := map[string]interface{}{"type": "workspaceWrite", "networkAccess": false}

	assert.Equal(t, "never", threadParams["approvalPolicy"])
	assert.Equal(t, wantSandbox, threadParams["sandboxPolicy"])
	assert.Equal(t, "never", turnParams["approvalPolicy"])
	assert.Equal(t, wantSandbox, turnParams["sandboxPolicy"])
	assertDoneEventually(t, proc.Done)
}

func TestCodexRunner_PolicyOverride_OnWire(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "capture-wire"), 15*time.Second)
	runner.ConfigureCodex(CodexRunnerOptions{
		ApprovalPolicy: "on-request",
		Sandbox:        map[string]interface{}{"type": "readOnly"},
	})

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	event := waitForEventType(t, proc.Events, "helper/requests")
	threadParams := eventDataMap(t, event, "threadStart")
	turnParams := eventDataMap(t, event, "turnStart")
	wantSandbox := map[string]interface{}{"type": "readOnly"}

	assert.Equal(t, "on-request", threadParams["approvalPolicy"])
	assert.Equal(t, wantSandbox, threadParams["sandboxPolicy"])
	assert.Equal(t, "on-request", turnParams["approvalPolicy"])
	assert.Equal(t, wantSandbox, turnParams["sandboxPolicy"])
	assertDoneEventually(t, proc.Done)
}

func TestCodexRunner_OverloadRetried(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "overload-once"), 15*time.Second)
	runner.overloadStartDelay = 0

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	event := waitForEventType(t, proc.Events, "helper/requests")
	counts := eventDataMap(t, event, "counts")
	assert.Equal(t, float64(2), counts["thread/start"])
	assertDoneEventually(t, proc.Done)
}

func TestCodexRunner_OverloadBudgetExhausted(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "overload-forever"), 15*time.Second)
	runner.overloadRetries = 2
	runner.overloadStartDelay = 0

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.Error(t, err)
	assert.Nil(t, proc)
	assert.ErrorIs(t, err, errCodexOverloaded)

	runner.mu.Lock()
	assert.Empty(t, runner.procs)
	runner.mu.Unlock()
}

func TestCodexRunner_LargeAgentMessage(t *testing.T) {
	runner := NewCodexRunner("unused", 5*time.Second)
	input := bytes.Buffer{}
	writeJSONLine(t, &input, map[string]interface{}{
		"method": "item/agentMessage",
		"params": map[string]interface{}{"text": strings.Repeat("x", 5*1024*1024)},
	})
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := startedExitedCodexProcess(t, ctx)
	eventsCh := make(chan types.AgentEvent, 512)
	go runner.streamEventsAndWait(process, bufio.NewReader(bytes.NewReader(input.Bytes())), eventsCh)

	events := collectEvents(t, eventsCh, process.done, 2, 10*time.Second)
	require.Len(t, events, 2)
	assert.Equal(t, "item/agentMessage", events[0].Type)
	text, ok := events[0].Data["text"].(string)
	require.True(t, ok)
	assert.Len(t, text, 5*1024*1024)
	assert.Equal(t, "turn/completed", events[1].Type)
	assertDoneEventually(t, process.done)
}

func TestCodexRunner_OversizedLineSkipped(t *testing.T) {
	runner := NewCodexRunner("unused", 5*time.Second)
	var logs bytes.Buffer
	runner.logger = log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel})
	input := bytes.Buffer{}
	input.WriteString(strings.Repeat("x", 64*1024*1024))
	input.WriteByte('\n')
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := startedExitedCodexProcess(t, ctx)
	eventsCh := make(chan types.AgentEvent, 512)
	go runner.streamEventsAndWait(process, bufio.NewReader(bytes.NewReader(input.Bytes())), eventsCh)

	events := collectEvents(t, eventsCh, process.done, 1, 10*time.Second)
	require.Len(t, events, 1)
	assert.Equal(t, "turn/completed", events[0].Type)
	assertDoneEventually(t, process.done)
	assert.Equal(t, 1, strings.Count(logs.String(), "maxStreamLineSize_exceeded"))
}

func TestCodexRunner_TerminalEventsNotDropped(t *testing.T) {
	runner := NewCodexRunner("unused", 2*time.Second)
	input := bytes.Buffer{}
	for i := 0; i < 50; i++ {
		writeJSONLine(t, &input, map[string]interface{}{
			"method": "item/test",
			"params": map[string]interface{}{"index": i},
		})
	}
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := startedExitedCodexProcess(t, ctx)
	events := make(chan types.AgentEvent, 4)

	go runner.streamEventsAndWait(process, bufio.NewReader(bytes.NewReader(input.Bytes())), events)
	time.Sleep(50 * time.Millisecond)

	var got []types.AgentEvent
	for event := range events {
		got = append(got, event)
	}
	require.NotEmpty(t, got)
	assert.Equal(t, "turn/completed", got[len(got)-1].Type)
	assertDoneEventually(t, process.done)
}

func TestCodexRunner_FlushErrorSurfaced(t *testing.T) {
	runner := NewCodexRunner("unused", 2*time.Second)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	defer writer.Close()

	err = runner.sendMessage(bufio.NewWriter(writer), map[string]interface{}{
		"id":     initializeRequestID,
		"method": "initialize",
		"params": map[string]interface{}{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flush")
}

func TestCodexRunner_HandshakeAndStreamTimeoutsIndependent(t *testing.T) {
	t.Run("handshake stalls", func(t *testing.T) {
		handshakeTimeout := 150 * time.Millisecond
		runner := NewCodexRunner(helperCommand(t, "silent-handshake"), handshakeTimeout).
			WithStreamReadTimeout(10 * time.Millisecond)

		start := time.Now()
		proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Nil(t, proc)
		assert.Contains(t, err.Error(), "handshake timeout")
		assert.GreaterOrEqual(t, elapsed, handshakeTimeout)
		assert.Less(t, elapsed, 2*time.Second)
	})

	t.Run("stream stalls", func(t *testing.T) {
		streamTimeout := 120 * time.Millisecond
		runner := NewCodexRunner(helperCommand(t, "stream-stall"), 5*time.Second).
			WithStreamReadTimeout(streamTimeout)

		proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
		require.NoError(t, err)

		start := time.Now()
		select {
		case doneErr := <-proc.Done:
			elapsed := time.Since(start)
			require.Error(t, doneErr)
			assert.ErrorIs(t, doneErr, errCodexStreamStalled)
			assert.GreaterOrEqual(t, elapsed, streamTimeout)
			assert.Less(t, elapsed, 2*time.Second)
		case <-time.After(2 * time.Second):
			t.Fatal("expected stream read timeout")
		}
	})
}

func TestCodexProtocolSequence(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "sequence"), 5*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	event := waitForEvent(t, proc.Events)
	require.Equal(t, "helper/sequence", event.Type)

	methodsRaw, ok := event.Data["methods"]
	require.True(t, ok)
	methodsAny, ok := methodsRaw.([]interface{})
	require.True(t, ok)

	methods := make([]string, 0, len(methodsAny))
	for _, m := range methodsAny {
		methods = append(methods, fmt.Sprint(m))
	}

	assert.Equal(t, []string{"initialize", "initialized", "thread/start", "turn/start"}, methods)

	require.NoError(t, runner.Stop(proc))
	assertDoneEventually(t, proc.Done)
}

func TestEventParsing(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "events"), 5*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	events := collectEvents(t, proc.Events, proc.Done, 4, 5*time.Second)
	require.Len(t, events, 4)

	typesSeen := make([]string, 0, len(events))
	for _, ev := range events {
		typesSeen = append(typesSeen, ev.Type)
		assert.False(t, ev.Timestamp.IsZero())
	}

	assert.Equal(t, []string{
		"item/commandExecution/requestApproval",
		"turn/failed",
		"turn/cancelled",
		"turn/completed",
	}, typesSeen)
}

func TestTimeoutKillsProcess(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "hang"), 2*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	runner.handshakeTimeout = 100 * time.Millisecond

	require.NoError(t, runner.Stop(proc))

	select {
	case <-proc.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected process to be terminated")
	}
}

func TestProcessCrash(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "crash"), 2*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	select {
	case doneErr := <-proc.Done:
		require.Error(t, doneErr)
	case <-time.After(2 * time.Second):
		t.Fatal("expected process crash to be reported")
	}
}

func TestMalformedJSON(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "malformed"), 2*time.Second)
	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)
	protocolErr := waitForEvent(t, proc.Events)
	assert.Equal(t, "protocol/error", protocolErr.Type)
	event := waitForEvent(t, proc.Events)
	assert.Equal(t, "turn/completed", event.Type)
	select {
	case doneErr := <-proc.Done:
		assert.NoError(t, doneErr)
	case <-time.After(2 * time.Second):
		t.Fatal("expected process to exit normally")
	}
}

func TestCodexRunner_ConcurrentStartStop(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "stderr-race"), 2*time.Second)
	workspace := t.TempDir()

	const attempts = 100

	var wg sync.WaitGroup
	errCh := make(chan error, attempts)

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			proc, err := runner.Start(context.Background(), types.Issue{ID: fmt.Sprintf("MT-%d", idx), Title: "Concurrent start-stop"}, workspace, "hello")
			if err != nil {
				errCh <- fmt.Errorf("start failed: %w", err)
				return
			}

			stopDone := make(chan error, 1)
			go func() {
				stopDone <- runner.Stop(proc)
			}()

			select {
			case err := <-stopDone:
				if err != nil {
					errCh <- fmt.Errorf("stop failed: %w", err)
					return
				}
			case <-time.After(2 * time.Second):
				errCh <- errors.New("stop timed out")
				return
			}

			select {
			case <-proc.Done:
			case <-time.After(2 * time.Second):
				errCh <- errors.New("done timed out")
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
}

func TestCodexRunner_StopWithFullEventBuffer(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "flood-events"), 3*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- runner.Stop(proc)
	}()

	select {
	case stopErr := <-stopDone:
		require.NoError(t, stopErr)
	case <-time.After(5 * time.Second):
		t.Fatal("expected Stop to return within 5 seconds")
	}

	select {
	case <-proc.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("expected done channel to be signaled")
	}
}

func TestCodexRunner_HandshakeTimeout(t *testing.T) {
	timeout := 150 * time.Millisecond
	runner := NewCodexRunner(helperCommand(t, "silent-handshake"), timeout)

	start := time.Now()
	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Nil(t, proc)
	assert.Contains(t, err.Error(), "handshake timeout")
	assert.GreaterOrEqual(t, elapsed, timeout)
	assert.Less(t, elapsed, 2*time.Second)
}

func TestCodexRunner_LargeJSONLLine(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "large-line"), 5*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	events := collectEvents(t, proc.Events, proc.Done, 2, 10*time.Second)
	require.Len(t, events, 2)

	assert.Equal(t, "helper/large-line", events[0].Type)
	payload, ok := events[0].Data["payload"].(string)
	require.True(t, ok)
	assert.Greater(t, len(payload), 2*1024*1024, "payload should be >2MB to verify large line handling")

	assert.Equal(t, "turn/completed", events[1].Type)

	assertDoneEventually(t, proc.Done)
}

func TestCodexRunner_MalformedJSONEmitsProtocolError(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "malformed"), 2*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	events := collectEvents(t, proc.Events, proc.Done, 2, 5*time.Second)
	require.Len(t, events, 2)

	// First event must be the protocol error
	assert.Equal(t, "protocol/error", events[0].Type)
	errMsg, ok := events[0].Data["error"].(string)
	require.True(t, ok)
	assert.Contains(t, errMsg, "malformed JSON")
	raw, ok := events[0].Data["raw"].(string)
	require.True(t, ok)
	assert.Contains(t, raw, "this is not json")

	// Second event: valid turn/completed after the malformed line
	assert.Equal(t, "turn/completed", events[1].Type)

	assertDoneEventually(t, proc.Done)
}

func TestCodexRunner_TimestampedUpdatesForwardedToRecipient(t *testing.T) {
	t.Run("core_test.exs", func(t *testing.T) {
		runner := NewCodexRunner(helperCommand(t, "timestamped-updates"), 2*time.Second)

		proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-35", Identifier: "CORE-422", Title: "Continuation update"}, t.TempDir(), "hello")
		require.NoError(t, err)

		events := collectEvents(t, proc.Events, proc.Done, 2, 5*time.Second)
		require.Len(t, events, 2)

		assert.Equal(t, "turn/update", events[0].Type)
		assert.False(t, events[0].Timestamp.IsZero())
		assert.Equal(t, "recipient-1", events[0].Data["recipient"])
		assert.Equal(t, "2026-03-05T12:34:56Z", events[0].Data["timestamp"])
		assert.Equal(t, "follow-up turn still active", events[0].Data["message"])

		assert.Equal(t, "turn/completed", events[1].Type)
		assertDoneEventually(t, proc.Done)
	})
}

func TestCodexRunner_ApprovalPolicyNeverAutoApproves(t *testing.T) {
	t.Run("app_server_test.exs", func(t *testing.T) {
		runner := NewCodexRunner(helperCommand(t, "approval-never"), 2*time.Second)

		proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-35", Identifier: "APP-321", Title: "Approval policy never"}, t.TempDir(), "hello")
		require.NoError(t, err)

		events := collectEvents(t, proc.Events, proc.Done, 3, 5*time.Second)
		require.Len(t, events, 3)

		assert.Equal(t, "item/commandExecution/requestApproval", events[0].Type)
		assert.Equal(t, "helper/sequence", events[1].Type)
		methodsRaw, ok := events[1].Data["methods"].([]interface{})
		require.True(t, ok)

		methods := make([]string, 0, len(methodsRaw))
		for _, method := range methodsRaw {
			methods = append(methods, fmt.Sprint(method))
		}

		assert.Equal(t, []string{"initialize", "initialized", "thread/start", "turn/start"}, methods)
		assert.Equal(t, "turn/completed", events[2].Type)
		assertDoneEventually(t, proc.Done)
	})
}

func TestCodexRunner_StderrForwardedToLogger(t *testing.T) {
	t.Run("app_server_test.exs", func(t *testing.T) {
		runner := NewCodexRunner(helperCommand(t, "stderr-crash"), 2*time.Second)
		var logs bytes.Buffer
		runner.logger = log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel})

		proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-35", Identifier: "APP-500", Title: "stderr forwarding"}, t.TempDir(), "hello")
		require.NoError(t, err)

		select {
		case doneErr := <-proc.Done:
			require.Error(t, doneErr)
			assert.Contains(t, doneErr.Error(), "stderr: codex stderr side output")
		case <-time.After(3 * time.Second):
			t.Fatal("expected process crash to be reported")
		}

		assert.NotNil(t, runner.logger)
	})
}

func TestCodexRunner_EmptyBinaryPath(t *testing.T) {
	tests := []struct {
		name       string
		binaryPath string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tabs and spaces", "\t  \t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewCodexRunner(tt.binaryPath, 5*time.Second)
			proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-1", Title: "Test"}, t.TempDir(), "hello")
			require.Error(t, err)
			assert.Nil(t, proc)
			assert.Contains(t, err.Error(), "codex binary path is empty")
		})
	}
}

func TestCodexRunner_RPCError(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "rpc-error"), 5*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-1", Title: "Test"}, t.TempDir(), "hello")
	require.Error(t, err)
	assert.Nil(t, proc)
	assert.Contains(t, err.Error(), "rpc error for id 1")
}

func TestRPCIDEquals_AllTypes(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		requestID int
		want      bool
	}{
		{"float64 match", float64(1), 1, true},
		{"float64 no match", float64(2), 1, false},
		{"float64 fractional", float64(1.5), 1, false},
		{"int match", int(1), 1, true},
		{"int no match", int(2), 1, false},
		{"int64 match", int64(1), 1, true},
		{"int64 no match", int64(2), 1, false},
		{"string match", "1", 1, true},
		{"string no match", "2", 1, false},
		{"string non-numeric", "abc", 1, false},
		{"nil value", nil, 1, false},
		{"bool value", true, 1, false},
		{"slice value", []int{1}, 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rpcIDEquals(tt.value, tt.requestID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCodexRunner_StopNilProcess(t *testing.T) {
	runner := NewCodexRunner("/nonexistent", 5*time.Second)
	err := runner.Stop(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "process is nil")
}

func TestCodexRunner_WithStderr_NilAndEmpty(t *testing.T) {
	runner := NewCodexRunner("/nonexistent", 5*time.Second)
	baseErr := errors.New("base error")

	t.Run("nil error returns nil", func(t *testing.T) {
		result := runner.withStderr(nil, &safeBuffer{})
		assert.NoError(t, result)
	})

	t.Run("nil stderr returns original error", func(t *testing.T) {
		result := runner.withStderr(baseErr, nil)
		assert.Equal(t, baseErr, result)
	})

	t.Run("empty stderr returns original error", func(t *testing.T) {
		result := runner.withStderr(baseErr, &safeBuffer{})
		assert.Equal(t, baseErr, result)
	})

	t.Run("whitespace-only stderr returns original error", func(t *testing.T) {
		buf := &safeBuffer{}
		_, _ = buf.Write([]byte("   \n\t  "))
		result := runner.withStderr(baseErr, buf)
		assert.Equal(t, baseErr, result)
	})

	t.Run("non-empty stderr wraps error", func(t *testing.T) {
		buf := &safeBuffer{}
		_, _ = buf.Write([]byte("something went wrong"))
		result := runner.withStderr(baseErr, buf)
		assert.ErrorIs(t, result, baseErr)
		assert.Contains(t, result.Error(), "stderr: something went wrong")
	})
}

func helperCommand(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)

	script := filepath.Join(t.TempDir(), "mock-codex.sh")
	content := fmt.Sprintf("#!/bin/sh\nexec env GO_WANT_HELPER_PROCESS=1 CODEX_HELPER_MODE=%s %s -test.run=TestCodexHelperProcess --\n", mode, exe)
	require.NoError(t, os.WriteFile(script, []byte(content), 0o755))

	return script
}

func waitForEvent(t *testing.T, ch <-chan types.AgentEvent) types.AgentEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
		return types.AgentEvent{}
	}
}

func waitForEventType(t *testing.T, ch <-chan types.AgentEvent, eventType string) types.AgentEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == eventType {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event type %q", eventType)
			return types.AgentEvent{}
		}
	}
}

func eventDataMap(t *testing.T, event types.AgentEvent, key string) map[string]interface{} {
	t.Helper()
	value, ok := event.Data[key].(map[string]interface{})
	require.True(t, ok, "expected event data %q to be an object", key)
	return value
}

// trackingWriteCloser is an in-memory stdin substitute that counts how
// many times Close() was invoked. Used by T8 tests to assert that
// closeStdin's sync.Once guard fires exactly once even when the
// runner observes multiple terminal events.
type trackingWriteCloser struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	closeCount int
}

func (w *trackingWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *trackingWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeCount++
	return nil
}

func (w *trackingWriteCloser) CloseCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeCount
}

// startedExitedCodexProcessWithStdin mirrors startedExitedCodexProcess
// but installs a trackingWriteCloser as stdin so tests can observe
// closeStdin invocations.
func startedExitedCodexProcessWithStdin(t *testing.T, ctx context.Context) (*codexProcess, *trackingWriteCloser) {
	t.Helper()
	stdin := &trackingWriteCloser{}
	cmd := exec.CommandContext(ctx, "sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	stderrDone := make(chan struct{})
	close(stderrDone)
	return &codexProcess{
		cmd:        cmd,
		stdin:      stdin,
		streamCtx:  ctx,
		done:       make(chan error, 1),
		stderr:     &safeBuffer{},
		stderrDone: stderrDone,
	}, stdin
}

func TestCodexRunner_StdinClosesAfterTerminalEvent(t *testing.T) {
	runner := NewCodexRunner("unused", 5*time.Second)
	input := bytes.Buffer{}
	writeJSONLine(t, &input, map[string]interface{}{"method": "item/started", "params": map[string]interface{}{}})
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, stdin := startedExitedCodexProcessWithStdin(t, ctx)
	eventsCh := make(chan types.AgentEvent, 16)
	go runner.streamEventsAndWait(process, bufio.NewReader(bytes.NewReader(input.Bytes())), eventsCh)

	events := collectEvents(t, eventsCh, process.done, 2, 5*time.Second)
	require.Len(t, events, 2)
	assert.Equal(t, "turn/completed", events[1].Type)
	assertDoneEventually(t, process.done)

	assert.Equal(t, 1, stdin.CloseCount(),
		"closeStdin must fire exactly once after a terminal event")
}

func TestCodexRunner_StdinCloseIsIdempotent(t *testing.T) {
	runner := NewCodexRunner("unused", 5*time.Second)
	input := bytes.Buffer{}
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
	writeJSONLine(t, &input, map[string]interface{}{"method": "turn/cancelled", "params": map[string]interface{}{}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, stdin := startedExitedCodexProcessWithStdin(t, ctx)
	eventsCh := make(chan types.AgentEvent, 16)
	go runner.streamEventsAndWait(process, bufio.NewReader(bytes.NewReader(input.Bytes())), eventsCh)

	events := collectEvents(t, eventsCh, process.done, 2, 5*time.Second)
	require.Len(t, events, 2)
	assertDoneEventually(t, process.done)

	assert.Equal(t, 1, stdin.CloseCount(),
		"two terminal events back-to-back must still close stdin only once")

	// Calling closeStdin manually a third time must remain a no-op.
	process.closeStdin()
	assert.Equal(t, 1, stdin.CloseCount(),
		"sync.Once guard must hold across direct closeStdin calls")
}

func writeJSONLine(t *testing.T, buf *bytes.Buffer, v map[string]interface{}) {
	t.Helper()
	payload, err := json.Marshal(v)
	require.NoError(t, err)
	buf.Write(payload)
	buf.WriteByte('\n')
}

func startedExitedCodexProcess(t *testing.T, ctx context.Context) *codexProcess {
	t.Helper()
	cmd := exec.CommandContext(ctx, "sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	stderrDone := make(chan struct{})
	close(stderrDone)
	return &codexProcess{
		cmd:        cmd,
		streamCtx:  ctx,
		done:       make(chan error, 1),
		stderr:     &safeBuffer{},
		stderrDone: stderrDone,
	}
}

func collectEvents(t *testing.T, events <-chan types.AgentEvent, done <-chan error, expected int, timeout time.Duration) []types.AgentEvent {
	t.Helper()
	out := make([]types.AgentEvent, 0, expected)
	deadline := time.After(timeout)

	for len(out) < expected {
		select {
		case ev := <-events:
			out = append(out, ev)
		case err := <-done:
			if err != nil {
				t.Fatalf("process terminated before all events arrived: %v", err)
			}
			for len(out) < expected {
				select {
				case ev := <-events:
					out = append(out, ev)
				case <-time.After(500 * time.Millisecond):
					return out
				}
			}
			return out
		case <-deadline:
			t.Fatalf("timed out collecting events, got %d", len(out))
		}
	}

	return out
}

func assertDoneEventually(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done channel was not signaled")
	}
}

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Getenv("CODEX_HELPER_MODE")
	require.NotEmpty(t, mode)

	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 0, 64*1024), maxJSONLineSize)
	writer := bufio.NewWriter(os.Stdout)

	var methods []string
	counts := map[string]int{}
	var threadStartParams map[string]interface{}
	var turnStartParams map[string]interface{}

	for reader.Scan() {
		line := reader.Text()
		msg := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		method, _ := msg["method"].(string)
		if method != "" {
			methods = append(methods, method)
			counts[method]++
		}

		switch method {
		case "initialize":
			if mode == "silent-handshake" {
				continue
			}
			if mode == "rpc-error" {
				writeJSON(t, writer, map[string]interface{}{
					"id":    msg["id"],
					"error": map[string]interface{}{"code": -32600, "message": "invalid request"},
				})
				return
			}
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"ok": true}})
		case "initialized":
		case "thread/start":
			if params, ok := msg["params"].(map[string]interface{}); ok {
				threadStartParams = params
			}
			if mode == "overload-once" && counts["thread/start"] == 1 {
				writeJSON(t, writer, map[string]interface{}{
					"id":    msg["id"],
					"error": map[string]interface{}{"code": -32001, "message": "Server overloaded"},
				})
				continue
			}
			if mode == "overload-forever" {
				writeJSON(t, writer, map[string]interface{}{
					"id":    msg["id"],
					"error": map[string]interface{}{"code": -32001, "message": "Server overloaded"},
				})
				continue
			}
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-1"}}})
		case "turn/start":
			if params, ok := msg["params"].(map[string]interface{}); ok {
				turnStartParams = params
			}
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-1"}}})

			switch mode {
			case "capture-wire", "overload-once":
				writeJSON(t, writer, map[string]interface{}{
					"method": "helper/requests",
					"params": map[string]interface{}{
						"counts":      counts,
						"threadStart": threadStartParams,
						"turnStart":   turnStartParams,
					},
				})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "sequence":
				writeJSON(t, writer, map[string]interface{}{"method": "helper/sequence", "params": map[string]interface{}{"methods": methods}})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "events":
				writeJSON(t, writer, map[string]interface{}{"method": "item/commandExecution/requestApproval", "params": map[string]interface{}{"command": "ls"}})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/failed", "params": map[string]interface{}{"message": "boom"}})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/cancelled", "params": map[string]interface{}{"reason": "cancelled"}})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{"usage": map[string]interface{}{"total_tokens": 12}}})
				return
			case "hang":
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
				defer signal.Stop(sigCh)
				for {
					select {
					case <-sigCh:
					}
				}
			case "crash":
				os.Exit(7)
			case "malformed":
				_, err := writer.WriteString("this is not json\n")
				require.NoError(t, err)
				require.NoError(t, writer.Flush())
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "large-line":
				bigPayload := strings.Repeat("x", 3*1024*1024)
				writeJSON(t, writer, map[string]interface{}{
					"method": "helper/large-line",
					"params": map[string]interface{}{
						"payload": bigPayload,
					},
				})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "stream-stall":
				time.Sleep(10 * time.Second)
				return
			case "stderr-race":
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
				defer signal.Stop(sigCh)

				stopWriter := make(chan struct{})
				go func() {
					for {
						select {
						case <-stopWriter:
							return
						default:
							_, _ = fmt.Fprintln(os.Stderr, "helper stderr line")
						}
					}
				}()

				<-sigCh
				close(stopWriter)
				os.Exit(3)
			case "flood-events":
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
				defer signal.Stop(sigCh)

				go func() {
					<-sigCh
					os.Exit(0)
				}()

				for i := 0; i < 4096; i++ {
					writeJSON(t, writer, map[string]interface{}{
						"method": "helper/flood",
						"params": map[string]interface{}{"index": i},
					})
				}

				select {}
			case "timestamped-updates":
				writeJSON(t, writer, map[string]interface{}{
					"method": "turn/update",
					"params": map[string]interface{}{
						"recipient": "recipient-1",
						"timestamp": "2026-03-05T12:34:56Z",
						"message":   "follow-up turn still active",
					},
				})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "approval-never":
				writeJSON(t, writer, map[string]interface{}{
					"method": "item/commandExecution/requestApproval",
					"params": map[string]interface{}{"command": "npm publish"},
				})
				writeJSON(t, writer, map[string]interface{}{"method": "helper/sequence", "params": map[string]interface{}{"methods": methods}})
				writeJSON(t, writer, map[string]interface{}{"method": "turn/completed", "params": map[string]interface{}{}})
				return
			case "stderr-crash":
				_, _ = fmt.Fprintln(os.Stderr, "codex stderr side output")
				os.Exit(23)
			default:
				os.Exit(9)
			}
		}
	}

	if err := reader.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		os.Exit(10)
	}
}

func writeJSON(t *testing.T, writer *bufio.Writer, v map[string]interface{}) {
	t.Helper()
	bytes, err := json.Marshal(v)
	require.NoError(t, err)
	_, err = writer.Write(append(bytes, '\n'))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())
}

// TestCodexRunner_BuildConfigOverrideArgs covers the contract between the
// workflow's codex.* fields and the `-c key=value` arguments that contrabass
// appends when spawning `codex app-server`. Without this plumbing, workflow
// settings were silently dropped and codex inherited whatever lived in the
// operator's ~/.codex/config.toml.
func TestCodexRunner_BuildConfigOverrideArgs(t *testing.T) {
	cases := []struct {
		name string
		opts CodexRunnerOptions
		want []string
	}{
		{
			name: "empty options produce no args",
			opts: CodexRunnerOptions{},
			want: nil,
		},
		{
			name: "model only",
			opts: CodexRunnerOptions{Model: "gpt-5-codex"},
			want: []string{"-c", `model="gpt-5-codex"`},
		},
		{
			name: "all three fields, in canonical order",
			opts: CodexRunnerOptions{
				Model:          "gpt-5-codex",
				ApprovalPolicy: "never",
				Sandbox:        "workspace-write",
			},
			want: []string{
				"-c", `model="gpt-5-codex"`,
				"-c", `approval_policy="never"`,
				"-c", `sandbox_mode="workspace-write"`,
			},
		},
		{
			name: "whitespace-only fields are skipped",
			opts: CodexRunnerOptions{Model: "  ", ApprovalPolicy: "never", Sandbox: ""},
			want: []string{"-c", `approval_policy="never"`},
		},
		{
			name: "values with spaces are quoted",
			opts: CodexRunnerOptions{Model: "gpt 5 codex"},
			want: []string{"-c", `model="gpt 5 codex"`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewCodexRunner("codex app-server", 5*time.Second)
			r.ConfigureCodex(c.opts)
			assert.Equal(t, c.want, r.buildConfigOverrideArgs())
		})
	}
}

// TestCodexRunner_ConfigureCodex_Chains checks that ConfigureCodex returns the
// receiver so call sites can chain it onto NewCodexRunner.
func TestCodexRunner_ConfigureCodex_Chains(t *testing.T) {
	base := NewCodexRunner("codex app-server", 5*time.Second)
	chained := base.ConfigureCodex(CodexRunnerOptions{Model: "gpt-5-codex"})
	assert.Same(t, base, chained, "ConfigureCodex must return the same runner pointer")
	assert.Equal(t, "gpt-5-codex", base.options.Model)
}
