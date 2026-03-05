package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
)

const (
	initializeRequestID  = 1
	threadStartRequestID = 2
	turnStartRequestID   = 3
)

type CodexRunner struct {
	binaryPath string
	timeout    time.Duration
	logger     *log.Logger

	mu    sync.Mutex
	procs map[int]*codexProcess
}

type codexProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	stderr *safeBuffer

	doneOnce sync.Once
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (p *codexProcess) finish(err error) {
	p.doneOnce.Do(func() {
		p.done <- err
		close(p.done)
	})
}

func NewCodexRunner(binaryPath string, timeout time.Duration) *CodexRunner {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &CodexRunner{
		binaryPath: binaryPath,
		timeout:    timeout,
		logger:     log.New(io.Discard, "", 0),
		procs:      make(map[int]*codexProcess),
	}
}

func (r *CodexRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	argv := strings.Fields(strings.TrimSpace(r.binaryPath))
	if len(argv) == 0 {
		return nil, errors.New("codex binary path is empty")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workspace

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex process: %w", err)
	}

	stderrBuf := &safeBuffer{}
	go func() {
		_, _ = io.Copy(stderrBuf, stderrPipe)
	}()

	process := &codexProcess{
		cmd:    cmd,
		stdin:  stdin,
		done:   make(chan error, 1),
		stderr: stderrBuf,
	}

	r.mu.Lock()
	r.procs[cmd.Process.Pid] = process
	r.mu.Unlock()

	writer := bufio.NewWriter(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	if err := r.sendMessage(writer, map[string]interface{}{
		"id":     initializeRequestID,
		"method": "initialize",
		"params": map[string]interface{}{
			"clientInfo": map[string]interface{}{
				"name":    "symphony-charm",
				"title":   "Symphony Charm",
				"version": "0.1.0",
			},
			"capabilities": map[string]interface{}{"experimentalApi": true},
		},
	}); err != nil {
		r.cleanupOnStartFailure(process)
		return nil, err
	}

	if _, err := r.awaitResponse(scanner, initializeRequestID); err != nil {
		r.cleanupOnStartFailure(process)
		return nil, r.withStderr(err, stderrBuf)
	}

	if err := r.sendMessage(writer, map[string]interface{}{
		"method": "initialized",
		"params": map[string]interface{}{},
	}); err != nil {
		r.cleanupOnStartFailure(process)
		return nil, err
	}

	if err := r.sendMessage(writer, map[string]interface{}{
		"id":     threadStartRequestID,
		"method": "thread/start",
		"params": map[string]interface{}{
			"cwd": workspace,
		},
	}); err != nil {
		r.cleanupOnStartFailure(process)
		return nil, err
	}

	threadResult, err := r.awaitResponse(scanner, threadStartRequestID)
	if err != nil {
		r.cleanupOnStartFailure(process)
		return nil, r.withStderr(err, stderrBuf)
	}

	threadID := extractNestedString(threadResult, "thread", "id")
	if threadID == "" {
		threadID = "unknown-thread"
	}

	if err := r.sendMessage(writer, map[string]interface{}{
		"id":     turnStartRequestID,
		"method": "turn/start",
		"params": map[string]interface{}{
			"threadId": threadID,
			"cwd":      workspace,
			"title":    fmt.Sprintf("%s: %s", issue.ID, issue.Title),
			"input": []map[string]interface{}{{
				"type": "text",
				"text": prompt,
			}},
		},
	}); err != nil {
		r.cleanupOnStartFailure(process)
		return nil, err
	}

	turnResult, err := r.awaitResponse(scanner, turnStartRequestID)
	if err != nil {
		r.cleanupOnStartFailure(process)
		return nil, r.withStderr(err, stderrBuf)
	}

	turnID := extractNestedString(turnResult, "turn", "id")
	sessionID := threadID
	if turnID != "" {
		sessionID = threadID + "-" + turnID
	}

	events := make(chan types.AgentEvent, 64)

	go r.streamEventsAndWait(process, scanner, events)

	return &AgentProcess{
		PID:       cmd.Process.Pid,
		SessionID: sessionID,
		Events:    events,
		Done:      process.done,
	}, nil
}

func (r *CodexRunner) Stop(proc *AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	r.mu.Lock()
	state, ok := r.procs[proc.PID]
	r.mu.Unlock()

	if !ok {
		return nil
	}

	if state.stdin != nil {
		_ = state.stdin.Close()
	}

	if state.cmd.Process != nil {
		_ = state.cmd.Process.Signal(os.Interrupt)
	}

	select {
	case <-state.done:
		return nil
	case <-time.After(r.timeout):
		if state.cmd.Process == nil {
			return nil
		}
		if err := state.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("kill process: %w", err)
		}
		select {
		case <-state.done:
		case <-time.After(r.timeout):
		}
		return nil
	}
}

func (r *CodexRunner) streamEventsAndWait(process *codexProcess, scanner *bufio.Scanner, events chan types.AgentEvent) {
	defer close(events)

	for scanner.Scan() {
		line := scanner.Text()
		msg := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			r.logger.Printf("codex malformed JSON: %v", err)
			continue
		}

		method, _ := msg["method"].(string)
		if method == "" {
			continue
		}

		data := map[string]interface{}{}
		if params, ok := msg["params"].(map[string]interface{}); ok {
			data = params
		}

		events <- types.AgentEvent{
			Type:      method,
			Data:      data,
			Timestamp: time.Now(),
		}
	}

	scanErr := scanner.Err()
	waitErr := process.cmd.Wait()
	if waitErr != nil {
		process.finish(r.withStderr(waitErr, process.stderr))
	} else if scanErr != nil {
		process.finish(scanErr)
	} else {
		process.finish(nil)
	}

	r.mu.Lock()
	delete(r.procs, process.cmd.Process.Pid)
	r.mu.Unlock()
}

func (r *CodexRunner) cleanupOnStartFailure(process *codexProcess) {
	if process.stdin != nil {
		_ = process.stdin.Close()
	}
	if process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
	}
	_, _ = process.cmd.Process.Wait()

	r.mu.Lock()
	delete(r.procs, process.cmd.Process.Pid)
	r.mu.Unlock()
}

func (r *CodexRunner) awaitResponse(scanner *bufio.Scanner, requestID int) (map[string]interface{}, error) {
	deadline := time.Now().Add(r.timeout)

	for time.Now().Before(deadline) {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}

		msg := map[string]interface{}{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			r.logger.Printf("codex malformed JSON while waiting response: %v", err)
			continue
		}

		id, ok := msg["id"]
		if !ok || !rpcIDEquals(id, requestID) {
			continue
		}

		if rpcErr, ok := msg["error"]; ok {
			return nil, fmt.Errorf("rpc error for id %d: %v", requestID, rpcErr)
		}

		if result, ok := msg["result"].(map[string]interface{}); ok {
			return result, nil
		}

		return map[string]interface{}{}, nil
	}

	return nil, fmt.Errorf("timeout waiting for response id=%d", requestID)
}

func (r *CodexRunner) sendMessage(writer *bufio.Writer, msg map[string]interface{}) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if _, err := writer.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush message: %w", err)
	}
	return nil
}

func (r *CodexRunner) withStderr(err error, stderr interface{ String() string }) error {
	if err == nil {
		return nil
	}
	if stderr == nil {
		return err
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return err
	}
	return fmt.Errorf("%w: stderr: %s", err, text)
}

func extractNestedString(m map[string]interface{}, k1 string, k2 string) string {
	inner, ok := m[k1].(map[string]interface{})
	if !ok {
		return ""
	}
	v, _ := inner[k2].(string)
	return v
}

func rpcIDEquals(value interface{}, requestID int) bool {
	switch v := value.(type) {
	case float64:
		return int(v) == requestID && v == float64(int(v))
	case int:
		return v == requestID
	case int64:
		return v == int64(requestID)
	case string:
		return v == strconv.Itoa(requestID)
	default:
		return false
	}
}
