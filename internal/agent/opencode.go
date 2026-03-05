package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/types"
)

type sseEvent struct {
	ID    string
	Event string
	Data  string
}

type sseReader struct {
	scanner     *bufio.Scanner
	lastEventID string
}

type OpenCodeRunner struct {
	binaryPath string
	port       int
	password   string
	username   string
	timeout    time.Duration
	logger     *log.Logger

	mu            sync.Mutex
	serverProcess *exec.Cmd
	serverURL     string
	httpClient    *http.Client
}

var _ AgentRunner = (*OpenCodeRunner)(nil)

func newSSEReader(r io.Reader) *sseReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	return &sseReader{scanner: scanner}
}

func (s *sseReader) Next() (*sseEvent, error) {
	event := &sseEvent{Event: "message", ID: s.lastEventID}
	dataLines := make([]string, 0, 4)
	hasData := false

	for {
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return nil, err
			}
			if !hasData {
				return nil, io.EOF
			}
			event.Data = strings.Join(dataLines, "\n")
			if event.Event == "" {
				event.Event = "message"
			}
			if event.ID == "" {
				event.ID = s.lastEventID
			}
			return event, nil
		}

		line := s.scanner.Text()
		if line == "" {
			if !hasData {
				event = &sseEvent{Event: "message", ID: s.lastEventID}
				dataLines = dataLines[:0]
				continue
			}
			event.Data = strings.Join(dataLines, "\n")
			if event.Event == "" {
				event.Event = "message"
			}
			if event.ID == "" {
				event.ID = s.lastEventID
			}
			return event, nil
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		field := line
		value := ""
		if idx := strings.Index(line, ":"); idx >= 0 {
			field = line[:idx]
			value = line[idx+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch field {
		case "event":
			event.Event = value
		case "data":
			dataLines = append(dataLines, value)
			hasData = true
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				event.ID = value
				s.lastEventID = value
			}
		case "retry":
			continue
		}
	}
}

func NewOpenCodeRunner(binaryPath string, port int, password, username string, timeout time.Duration) *OpenCodeRunner {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &OpenCodeRunner{
		binaryPath: binaryPath,
		port:       port,
		password:   password,
		username:   username,
		timeout:    timeout,
		logger:     log.NewWithOptions(io.Discard, log.Options{}),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *OpenCodeRunner) ensureServer(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.serverURL != "" {
		return nil
	}

	argv := strings.Fields(strings.TrimSpace(r.binaryPath))
	if len(argv) == 0 {
		return errors.New("opencode binary path is empty")
	}

	if r.port > 0 {
		argv = append(argv, "--port", strconv.Itoa(r.port))
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create opencode stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start opencode server: %w", err)
	}

	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "listening on http://") || strings.Contains(line, "listening on https://") {
				if url := extractListeningURL(line); url != "" {
					urlCh <- url
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		errCh <- errors.New("opencode server exited before emitting listening URL")
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ctx.Err()
	case <-timer.C:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("timed out waiting for opencode server startup after %s", r.timeout)
	case err := <-errCh:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("failed to detect opencode server URL: %w", err)
	case url := <-urlCh:
		r.serverURL = strings.TrimRight(url, "/")
		r.serverProcess = cmd
		return nil
	}
}

func (r *OpenCodeRunner) stopServer() error {
	r.mu.Lock()
	cmd := r.serverProcess
	r.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt opencode server: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		r.mu.Lock()
		r.serverProcess = nil
		r.serverURL = ""
		r.mu.Unlock()
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	case <-time.After(r.timeout):
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill opencode server: %w", err)
		}
		select {
		case <-waitCh:
		case <-time.After(r.timeout):
		}
		r.mu.Lock()
		r.serverProcess = nil
		r.serverURL = ""
		r.mu.Unlock()
		return nil
	}
}

func (r *OpenCodeRunner) Start(ctx context.Context, _ types.Issue, _ string, prompt string) (*AgentProcess, error) {
	if err := r.ensureServer(ctx); err != nil {
		return nil, err
	}

	serverURL, serverPID := r.serverState()

	sessionID, err := r.createSession(ctx, serverURL)
	if err != nil {
		return nil, err
	}

	events := make(chan types.AgentEvent, 128)
	done := make(chan error, 1)
	streamCtx, cancelStream := context.WithCancel(ctx)
	var doneOnce sync.Once

	finish := func(doneErr error) {
		doneOnce.Do(func() {
			done <- doneErr
			close(done)
			close(events)
		})
	}

	go r.streamSessionEvents(streamCtx, serverURL, sessionID, events, finish)

	if err := r.submitPrompt(ctx, serverURL, sessionID, prompt); err != nil {
		cancelStream()
		return nil, err
	}

	return &AgentProcess{
		PID:       serverPID,
		SessionID: sessionID,
		Events:    events,
		Done:      done,
	}, nil
}

func (r *OpenCodeRunner) Stop(proc *AgentProcess) error {
	if proc == nil {
		return errors.New("process is nil")
	}

	serverURL, _ := r.serverState()
	if serverURL == "" {
		return nil
	}

	_ = r.abortSession(context.Background(), serverURL, proc.SessionID)
	return nil
}

func (r *OpenCodeRunner) serverState() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pid := 0
	if r.serverProcess != nil && r.serverProcess.Process != nil {
		pid = r.serverProcess.Process.Pid
	}

	return r.serverURL, pid
}

func (r *OpenCodeRunner) createSession(ctx context.Context, serverURL string) (string, error) {
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session", nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("create opencode session failed: status %d body=%q", resp.StatusCode, string(body))
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}

	sessionID, _ := payload["id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("create session response missing id")
	}

	return sessionID, nil
}

func (r *OpenCodeRunner) submitPrompt(ctx context.Context, serverURL, sessionID, prompt string) error {
	body := map[string]interface{}{"content": prompt}
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session/"+sessionID+"/prompt_async", body, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("submit opencode prompt failed: status %d body=%q", resp.StatusCode, string(respBody))
	}

	return nil
}

func (r *OpenCodeRunner) abortSession(ctx context.Context, serverURL, sessionID string) error {
	resp, err := r.doRequest(ctx, http.MethodPost, serverURL+"/session/"+sessionID+"/abort", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("abort opencode session failed: status %d", resp.StatusCode)
	}

	return nil
}

func (r *OpenCodeRunner) streamSessionEvents(
	ctx context.Context,
	serverURL string,
	sessionID string,
	events chan<- types.AgentEvent,
	finish func(error),
) {
	defer finish(nil)

	headers := map[string]string{"Accept": "text/event-stream"}
	resp, err := r.doRequest(ctx, http.MethodGet, serverURL+"/event", nil, headers)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		finish(fmt.Errorf("connect opencode event stream: %w", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		finish(fmt.Errorf("opencode event stream failed: status %d body=%q", resp.StatusCode, string(body)))
		return
	}

	reader := newSSEReader(resp.Body)
	for {
		event, readErr := reader.Next()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || ctx.Err() != nil {
				return
			}
			finish(fmt.Errorf("read opencode event stream: %w", readErr))
			return
		}

		if event == nil || strings.TrimSpace(event.Data) == "" {
			continue
		}

		payload := map[string]interface{}{}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			r.logger.Warn("failed to parse opencode event payload", "err", err)
			continue
		}

		jsonEventType, _ := payload["type"].(string)
		eventType := event.Event
		if jsonEventType != "" {
			eventType = jsonEventType
		}
		if eventType == "" {
			eventType = "message"
		}

		if eventType == "server.heartbeat" {
			continue
		}

		if eventSessionID := eventPayloadSessionID(payload); eventSessionID != sessionID {
			continue
		}

		agentEvent := types.AgentEvent{
			Type:      eventType,
			Data:      payload,
			Timestamp: time.Now(),
		}

		select {
		case <-ctx.Done():
			return
		case events <- agentEvent:
		default:
		}

		if eventType == "session.error" {
			finish(errors.New("opencode session error"))
			return
		}

		if eventType == "session.status" && eventPayloadIdle(payload) {
			return
		}
	}
}

func (r *OpenCodeRunner) doRequest(
	ctx context.Context,
	method string,
	url string,
	body interface{},
	headers map[string]string,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if r.password != "" {
		req.SetBasicAuth(r.username, r.password)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform %s %s: %w", method, url, err)
	}

	return resp, nil
}

func eventPayloadSessionID(payload map[string]interface{}) string {
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return ""
	}
	sessionID, _ := properties["sessionID"].(string)
	return sessionID
}

func eventPayloadIdle(payload map[string]interface{}) bool {
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return false
	}

	status, ok := properties["status"].(map[string]interface{})
	if !ok {
		return false
	}

	statusType, _ := status["type"].(string)
	return statusType == "idle"
}

func extractListeningURL(line string) string {
	idx := strings.Index(line, "http://")
	if idx < 0 {
		idx = strings.Index(line, "https://")
	}
	if idx < 0 {
		return ""
	}

	urlPart := strings.TrimSpace(line[idx:])
	pieces := strings.Fields(urlPart)
	if len(pieces) == 0 {
		return ""
	}

	return strings.Trim(pieces[0], "\"'`")
}
