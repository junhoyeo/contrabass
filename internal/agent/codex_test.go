package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexProtocolSequence(t *testing.T) {
	runner := NewCodexRunner(helperCommand(t, "sequence"), 2*time.Second)

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
	runner := NewCodexRunner(helperCommand(t, "events"), 2*time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	events := collectEvents(t, proc.Events, proc.Done, 4, 2*time.Second)
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
	runner := NewCodexRunner(helperCommand(t, "hang"), 100*time.Millisecond)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "MT-11", Title: "Task 11"}, t.TempDir(), "hello")
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, runner.Stop(proc))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 3*time.Second)

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
	runner := NewCodexRunner(helperCommand(t, "stderr-race"), 500*time.Millisecond)
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return types.AgentEvent{}
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
	case <-time.After(2 * time.Second):
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
	reader.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	writer := bufio.NewWriter(os.Stdout)

	var methods []string

	for reader.Scan() {
		line := reader.Text()
		msg := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		method, _ := msg["method"].(string)
		if method != "" {
			methods = append(methods, method)
		}

		switch method {
		case "initialize":
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"ok": true}})
		case "initialized":
		case "thread/start":
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-1"}}})
		case "turn/start":
			writeJSON(t, writer, map[string]interface{}{"id": msg["id"], "result": map[string]interface{}{"turn": map[string]interface{}{"id": "turn-1"}}})

			switch mode {
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
