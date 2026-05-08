package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/types"
)

const (
	initializeRequestID  = 1
	threadStartRequestID = 2
	turnStartRequestID   = 3
	maxJSONLineSize      = 10 * 1024 * 1024 // 10MB
	maxStreamLineSize    = 32 * 1024 * 1024 // 32 MB
)

var (
	errCodexAlreadyStopped = errors.New("codex process already stopped")
	errCodexStopFailed     = errors.New("codex process stop failed")
	errCodexOverloaded     = errors.New("codex app-server overloaded (-32001)")
	errCodexReadTimeout    = errors.New("codex read timeout")
	errCodexStreamStalled  = errors.New("codex stream read timeout")
)

var terminalCodexEventTypes = map[string]struct{}{
	"turn/completed": {},
	"turn/failed":    {},
	"turn/cancelled": {},
}

type CodexRunner struct {
	binaryPath         string
	handshakeTimeout   time.Duration
	streamReadTimeout  time.Duration
	timeout            time.Duration
	logger             *log.Logger
	options            CodexRunnerOptions
	overloadRetries    int
	overloadRetryCap   time.Duration
	overloadStartDelay time.Duration

	mu    sync.Mutex
	procs map[int]*codexProcess
}

// CodexRunnerOptions captures workflow-driven settings that contrabass forwards
// to `codex app-server` as `-c key=value` overrides. Empty strings are skipped
// so codex falls back to whatever is in `~/.codex/config.toml`. The keys are
// codex TOML config paths — values are passed through unchanged, so callers
// must use values codex accepts (e.g. `model="gpt-5-codex"`,
// `approval_policy="never"`, `sandbox_mode="workspace-write"`).
type CodexRunnerOptions struct {
	Model          string
	ApprovalPolicy string
	Sandbox        interface{}
}

type codexProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	streamCtx  context.Context
	done       chan error
	stderr     *safeBuffer
	stderrDone chan struct{}

	doneOnce       sync.Once
	stdinCloseOnce sync.Once
}

// closeStdin closes the codex subprocess's stdin pipe at most once.
// Codex exec mode requires EOF on stdin to exit cleanly after a
// terminal turn event (turn/completed, turn/failed, turn/cancelled);
// without it the subprocess sits idle waiting for the next prompt and
// contrabass's stall detector synthesizes a spurious failure.
func (p *codexProcess) closeStdin() {
	p.stdinCloseOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
	})
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
		binaryPath:         binaryPath,
		handshakeTimeout:   timeout,
		timeout:            timeout,
		logger:             log.NewWithOptions(io.Discard, log.Options{}),
		overloadRetries:    5,
		overloadRetryCap:   4 * time.Second,
		overloadStartDelay: 100 * time.Millisecond,
		procs:              make(map[int]*codexProcess),
	}
}

func (r *CodexRunner) WithStreamReadTimeout(d time.Duration) *CodexRunner {
	r.streamReadTimeout = d
	return r
}

// ConfigureCodex attaches workflow-driven codex config that the runner will
// forward as `-c key=value` overrides each time it spawns the agent process.
// Returns the receiver to allow chaining. Empty fields are skipped.
func (r *CodexRunner) ConfigureCodex(opts CodexRunnerOptions) *CodexRunner {
	r.options = opts
	return r
}

// buildConfigOverrideArgs renders CodexRunnerOptions into a flat `-c key=value`
// argument list. TOML strings are double-quoted as expected by `codex -c`.
func (r *CodexRunner) buildConfigOverrideArgs() []string {
	var args []string
	add := func(key, value string) {
		v := strings.TrimSpace(value)
		if v == "" {
			return
		}
		args = append(args, "-c", fmt.Sprintf("%s=%q", key, v))
	}
	add("model", r.options.Model)
	add("approval_policy", r.options.ApprovalPolicy)
	if sandbox, ok := r.options.Sandbox.(string); ok {
		add("sandbox_mode", sandbox)
	}
	return args
}

// policyParams returns the approvalPolicy and sandboxPolicy values that
// thread/start and turn/start must carry. Workflow override wins; otherwise
// hardcoded defaults are returned.
func (r *CodexRunner) policyParams() (approval string, sandbox interface{}) {
	approval = strings.TrimSpace(r.options.ApprovalPolicy)
	if approval == "" {
		approval = "never"
	}

	sandbox = map[string]interface{}{
		"type":          "workspaceWrite",
		"networkAccess": false,
	}
	switch value := r.options.Sandbox.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			sandbox = value
		}
	case map[string]interface{}:
		if value != nil {
			sandbox = value
		}
	}

	return approval, sandbox
}

func (r *CodexRunner) Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error) {
	argv := strings.Fields(strings.TrimSpace(r.binaryPath))
	if len(argv) == 0 {
		return nil, errors.New("codex binary path is empty")
	}
	argv = append(argv, r.buildConfigOverrideArgs()...)

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
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderrBuf, stderrPipe)
		close(stderrDone)
	}()

	process := &codexProcess{
		cmd:        cmd,
		stdin:      stdin,
		streamCtx:  ctx,
		done:       make(chan error, 1),
		stderr:     stderrBuf,
		stderrDone: stderrDone,
	}

	r.mu.Lock()
	r.procs[cmd.Process.Pid] = process
	r.mu.Unlock()

	writer := bufio.NewWriter(stdin)
	reader := bufio.NewReader(stdout)
	approvalPolicy, sandboxPolicy := r.policyParams()

	initializeMsg := map[string]interface{}{
		"id":     initializeRequestID,
		"method": "initialize",
		"params": map[string]interface{}{
			"clientInfo": map[string]interface{}{
				"name":    "contrabass",
				"title":   "Contrabass",
				"version": "0.1.0",
			},
			"capabilities": map[string]interface{}{"experimentalApi": true},
		},
	}
	if _, err := r.handshakeStep(reader, writer, initializeMsg, initializeRequestID); err != nil {
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

	threadStartMsg := map[string]interface{}{
		"id":     threadStartRequestID,
		"method": "thread/start",
		"params": map[string]interface{}{
			"cwd":            workspace,
			"approvalPolicy": approvalPolicy,
			"sandboxPolicy":  sandboxPolicy,
		},
	}
	threadResult, err := r.handshakeStep(reader, writer, threadStartMsg, threadStartRequestID)
	if err != nil {
		r.cleanupOnStartFailure(process)
		return nil, r.withStderr(err, stderrBuf)
	}

	threadID := extractNestedString(threadResult, "thread", "id")
	if threadID == "" {
		threadID = "unknown-thread"
	}

	turnStartMsg := map[string]interface{}{
		"id":     turnStartRequestID,
		"method": "turn/start",
		"params": map[string]interface{}{
			"threadId":       threadID,
			"cwd":            workspace,
			"approvalPolicy": approvalPolicy,
			"sandboxPolicy":  sandboxPolicy,
			"title":          fmt.Sprintf("%s: %s", issue.ID, issue.Title),
			"input": []map[string]interface{}{{
				"type": "text",
				"text": prompt,
			}},
		},
	}
	turnResult, err := r.handshakeStep(reader, writer, turnStartMsg, turnStartRequestID)
	if err != nil {
		r.cleanupOnStartFailure(process)
		return nil, r.withStderr(err, stderrBuf)
	}

	turnID := extractNestedString(turnResult, "turn", "id")
	sessionID := threadID
	if turnID != "" {
		sessionID = threadID + "-" + turnID
	}

	events := make(chan types.AgentEvent, 512)

	go r.streamEventsAndWait(process, reader, events)

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
		return fmt.Errorf("%w: pid %d", errCodexAlreadyStopped, proc.PID)
	}

	state.closeStdin()

	if state.cmd.Process == nil {
		return fmt.Errorf("%w: pid %d", errCodexAlreadyStopped, proc.PID)
	}

	if err := state.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("%w: interrupt process: %w", errCodexStopFailed, err)
	}

	select {
	case doneErr := <-state.done:
		if doneErr != nil && !errors.Is(doneErr, os.ErrProcessDone) {
			r.logger.Warn("codex process exited with error during stop", "pid", proc.PID, "err", doneErr)
		}
		return nil
	case <-time.After(r.handshakeTimeout):
		if state.cmd.Process == nil {
			return fmt.Errorf("%w: pid %d", errCodexAlreadyStopped, proc.PID)
		}
		if err := state.cmd.Process.Kill(); err != nil {
			if errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("%w: pid %d", errCodexAlreadyStopped, proc.PID)
			}
			return fmt.Errorf("%w: kill process: %w", errCodexStopFailed, err)
		}
		select {
		case doneErr := <-state.done:
			if doneErr != nil && !errors.Is(doneErr, os.ErrProcessDone) {
				r.logger.Warn("codex process exited with error after kill", "pid", proc.PID, "err", doneErr)
			}
			return nil
		case <-time.After(r.handshakeTimeout):
			return fmt.Errorf("%w: timeout after kill", errCodexStopFailed)
		}
	}
}

// Close is a no-op for CodexRunner; each Codex process is per-session and
// cleaned up by Stop.
func (r *CodexRunner) Close() error { return nil }

func (r *CodexRunner) streamEventsAndWait(process *codexProcess, reader *bufio.Reader, events chan types.AgentEvent) {
	defer close(events)
	var readErr error

	for {
		line, err := r.readStreamLine(reader)
		if len(line) == 0 {
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				break
			}
			continue
		}

		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(line) > maxStreamLineSize {
			preview := line
			if len(preview) > 256 {
				preview = preview[:256]
			}
			r.logger.Warn(
				"codex stream line exceeded maximum size",
				"event", "maxStreamLineSize_exceeded",
				"line_bytes", len(line),
				"max_bytes", maxStreamLineSize,
				"preview", string(preview),
			)
			if err != nil && !errors.Is(err, io.EOF) {
				readErr = err
				break
			}
			continue
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if err != nil && !errors.Is(err, io.EOF) {
				readErr = err
				break
			}
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		msg := map[string]interface{}{}
		if err := json.Unmarshal(line, &msg); err != nil {
			rawPreview := string(line)
			if len(rawPreview) > 512 {
				rawPreview = rawPreview[:512] + "...(truncated)"
			}
			r.logger.Warn("codex malformed JSON", "err", err)
			event := types.AgentEvent{
				Type: "protocol/error",
				Data: map[string]interface{}{
					"error": fmt.Sprintf("malformed JSON: %s", err.Error()),
					"raw":   rawPreview,
				},
				Timestamp: time.Now(),
			}
			r.sendStreamEvent(process.streamCtx, events, event)
			continue
		}
		method, _ := msg["method"].(string)
		if method == "" {
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				break
			}
			continue
		}
		data := map[string]interface{}{}
		if params, ok := msg["params"].(map[string]interface{}); ok {
			data = params
		}

		event := types.AgentEvent{
			Type:      method,
			Data:      data,
			Timestamp: time.Now(),
		}
		r.sendStreamEvent(process.streamCtx, events, event)

		// codex exec idles on stdin after a terminal turn event. Closing
		// stdin signals end-of-input so codex finishes its bookkeeping
		// (notify-hook, exit) instead of letting contrabass's
		// stall_timeout synthesize a spurious failure. The Once guard
		// makes this safe even if the stream emits multiple terminal
		// events back-to-back.
		if _, terminal := terminalCodexEventTypes[event.Type]; terminal {
			process.closeStdin()
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}

	if isStreamReadTimeout(readErr) && process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
	}
	waitErr := process.cmd.Wait()
	<-process.stderrDone
	if waitErr != nil && !isStreamReadTimeout(readErr) {
		process.finish(r.withStderr(waitErr, process.stderr))
	} else if readErr != nil {
		process.finish(readErr)
	} else {
		process.finish(nil)
	}
	r.mu.Lock()
	delete(r.procs, process.cmd.Process.Pid)
	r.mu.Unlock()
}

func (r *CodexRunner) sendStreamEvent(streamCtx context.Context, events chan types.AgentEvent, event types.AgentEvent) {
	if streamCtx == nil {
		streamCtx = context.Background()
	}
	if _, terminal := terminalCodexEventTypes[event.Type]; terminal {
		select {
		case events <- event:
		case <-streamCtx.Done():
			r.logger.Debug("codex stream context cancelled before terminal event delivery", "event", event.Type, "err", streamCtx.Err())
		}
		return
	}

	select {
	case events <- event:
	default:
	}
}

func (r *CodexRunner) cleanupOnStartFailure(process *codexProcess) {
	process.closeStdin()
	if process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
	}
	_ = process.cmd.Wait()
	<-process.stderrDone

	r.mu.Lock()
	if process.cmd.Process != nil {
		delete(r.procs, process.cmd.Process.Pid)
	}
	r.mu.Unlock()
}

func (r *CodexRunner) awaitResponse(reader *bufio.Reader, requestID int) (map[string]interface{}, error) {
	deadline := time.Now().Add(r.handshakeTimeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("handshake timeout waiting for response id=%d after %s", requestID, r.handshakeTimeout)
		}

		line, err := r.readLineWithTimeout(reader, remaining)
		if errors.Is(err, errCodexReadTimeout) {
			return nil, fmt.Errorf("handshake timeout waiting for read after %s", remaining)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}

		msg := map[string]interface{}{}
		if err := json.Unmarshal(trimmed, &msg); err != nil {
			r.logger.Warn("codex malformed JSON while waiting response", "err", err)
			continue
		}

		id, ok := msg["id"]
		if !ok || !rpcIDEquals(id, requestID) {
			continue
		}

		if rpcErr, ok := msg["error"]; ok {
			if isCodexOverloadRPCError(rpcErr) {
				return nil, errCodexOverloaded
			}
			return nil, fmt.Errorf("rpc error for id %d: %v", requestID, rpcErr)
		}

		if result, ok := msg["result"].(map[string]interface{}); ok {
			return result, nil
		}

		return map[string]interface{}{}, nil
	}
}

func (r *CodexRunner) handshakeStep(reader *bufio.Reader, writer *bufio.Writer, msg map[string]interface{}, requestID int) (map[string]interface{}, error) {
	var lastErr error
	for attempt := 0; attempt <= r.overloadRetries; attempt++ {
		if err := r.sendMessage(writer, msg); err != nil {
			return nil, err
		}
		result, err := r.awaitResponse(reader, requestID)
		if !isOverloadError(err) {
			return result, err
		}
		lastErr = err
		if attempt == r.overloadRetries {
			break
		}

		delay := r.overloadRetryDelay(attempt)
		r.logger.Warn("codex app-server overloaded; retrying handshake", "id", requestID, "attempt", attempt+1, "delay", delay)
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("codex handshake id=%d overload retries exhausted: %w", requestID, lastErr)
}

func (r *CodexRunner) overloadRetryDelay(attempt int) time.Duration {
	if r.overloadStartDelay <= 0 {
		return 0
	}
	if r.overloadRetryCap <= 0 {
		return r.overloadStartDelay
	}
	if attempt >= 4 {
		return r.overloadRetryCap
	}
	delay := r.overloadStartDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	if delay > r.overloadRetryCap {
		return r.overloadRetryCap
	}
	return delay
}

func isCodexOverloadRPCError(rpcErr interface{}) bool {
	errMap, ok := rpcErr.(map[string]interface{})
	if !ok {
		return false
	}
	code, ok := errMap["code"]
	if !ok {
		return false
	}
	return rpcIDEquals(code, -32001)
}

func isOverloadError(err error) bool {
	return errors.Is(err, errCodexOverloaded)
}

func (r *CodexRunner) readLineWithTimeout(reader *bufio.Reader, timeout time.Duration) ([]byte, error) {
	type lineReadResult struct {
		line []byte
		err  error
	}

	resultCh := make(chan lineReadResult, 1)
	go func() {
		// bufio.Reader reads cannot be interrupted; this unblocks when process stdout closes.
		line, err := reader.ReadBytes('\n')
		resultCh <- lineReadResult{line: line, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-resultCh:
		return result.line, result.err
	case <-timer.C:
		return nil, fmt.Errorf("%w after %s", errCodexReadTimeout, timeout)
	}
}

func (r *CodexRunner) readStreamLine(reader *bufio.Reader) ([]byte, error) {
	if r.streamReadTimeout <= 0 {
		return reader.ReadBytes('\n')
	}
	line, err := r.readLineWithTimeout(reader, r.streamReadTimeout)
	if errors.Is(err, errCodexReadTimeout) {
		return line, fmt.Errorf("%w after %s", errCodexStreamStalled, r.streamReadTimeout)
	}
	return line, err
}

func isStreamReadTimeout(err error) bool {
	return errors.Is(err, errCodexStreamStalled)
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
