package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const paneListFormat = "#{pane_id}:#{pane_index}:#{pane_active}:#{pane_dead}:#{pane_pid}:#{pane_title}"

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultRunner executes commands via os/exec.
type DefaultRunner struct{}

// Run executes a command and returns combined output.
func (r DefaultRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// Session represents a tmux session handle.
type Session struct {
	Name   string
	runner CommandRunner
}

// PaneInfo holds information about a tmux pane.
type PaneInfo struct {
	ID     string
	Index  int
	Active bool
	Dead   bool
	PID    int
	Title  string
}

// NewSession creates a session handle without creating the tmux session.
func NewSession(teamName string, runner CommandRunner) *Session {
	if runner == nil {
		runner = DefaultRunner{}
	}

	return &Session{
		Name:   "contrabass-" + teamName,
		runner: runner,
	}
}

// Create creates the tmux session and returns an error if it already exists.
func (s *Session) Create(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if s.Name == "" {
		return fmt.Errorf("session name is empty")
	}

	if s.IsAlive(ctx) {
		return fmt.Errorf("tmux session %q already exists", s.Name)
	}

	if _, err := s.runTmux(ctx, "new-session", "-d", "-s", s.Name); err != nil {
		return fmt.Errorf("create tmux session %q: %w", s.Name, err)
	}

	return nil
}

// CreateIfNotExists creates the session when it does not already exist.
func (s *Session) CreateIfNotExists(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if s.Name == "" {
		return fmt.Errorf("session name is empty")
	}

	if s.IsAlive(ctx) {
		return nil
	}

	return s.Create(ctx)
}

// Kill destroys the tmux session and all its panes.
func (s *Session) Kill(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if s.Name == "" {
		return fmt.Errorf("session name is empty")
	}

	if _, err := s.runTmux(ctx, "kill-session", "-t", s.exactTarget()); err != nil {
		return fmt.Errorf("kill tmux session %q: %w", s.Name, err)
	}

	return nil
}

// IsAlive reports whether the tmux session exists.
func (s *Session) IsAlive(ctx context.Context) bool {
	if s == nil || s.Name == "" {
		return false
	}

	_, err := s.runTmux(ctx, "has-session", "-t", s.exactTarget())
	return err == nil
}

// exactTarget returns the session name with tmux's "=" exact-match prefix.
// Without it tmux falls back to prefix matching, so operations on team
// "alpha" could target team "alpha-2"'s session.
func (s *Session) exactTarget() string {
	return "=" + s.Name
}

// ListPanes returns all panes for this session.
func (s *Session) ListPanes(ctx context.Context) ([]PaneInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("session is nil")
	}

	output, err := s.runTmux(ctx, "list-panes", "-t", s.exactTarget(), "-F", paneListFormat)
	if err != nil {
		return nil, fmt.Errorf("list panes for session %q: %w", s.Name, err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []PaneInfo{}, nil
	}

	lines := strings.Split(trimmed, "\n")
	panes := make([]PaneInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 6)
		if len(parts) != 6 {
			return nil, fmt.Errorf("parse tmux pane line %q: invalid format", line)
		}

		index, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("parse pane index from %q: %w", line, err)
		}

		pid, err := strconv.Atoi(parts[4])
		if err != nil {
			return nil, fmt.Errorf("parse pane pid from %q: %w", line, err)
		}

		panes = append(panes, PaneInfo{
			ID:     parts[0],
			Index:  index,
			Active: parts[2] == "1",
			Dead:   parts[3] == "1",
			PID:    pid,
			Title:  parts[5],
		})
	}

	return panes, nil
}

// SplitPane splits a target pane and returns the new pane ID.
func (s *Session) SplitPane(ctx context.Context, targetPane string, horizontal bool) (string, error) {
	if s == nil {
		return "", fmt.Errorf("session is nil")
	}
	if targetPane == "" {
		return "", fmt.Errorf("target pane is empty")
	}

	direction := "-v"
	if horizontal {
		direction = "-h"
	}

	output, err := s.runTmux(ctx, "split-window", "-t", targetPane, direction, "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("split pane %q: %w", targetPane, err)
	}

	newPaneID := strings.TrimSpace(string(output))
	if newPaneID == "" {
		return "", fmt.Errorf("split pane %q: empty pane id", targetPane)
	}

	return newPaneID, nil
}

// NewWindow creates a new window and returns the first pane ID.
func (s *Session) NewWindow(ctx context.Context, windowName string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("session is nil")
	}
	if windowName == "" {
		return "", fmt.Errorf("window name is empty")
	}

	output, err := s.runTmux(ctx, "new-window", "-t", s.exactTarget(), "-n", windowName, "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("create tmux window %q: %w", windowName, err)
	}

	paneID := strings.TrimSpace(string(output))
	if paneID == "" {
		return "", fmt.Errorf("create tmux window %q: empty pane id", windowName)
	}

	return paneID, nil
}

// SendKeys sends text and special keys to a pane.
func (s *Session) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if paneID == "" {
		return fmt.Errorf("pane id is empty")
	}
	if len(keys) == 0 {
		return fmt.Errorf("keys are empty")
	}

	args := make([]string, 0, 4+len(keys))
	args = append(args, "send-keys", "-t", paneID)
	args = append(args, keys...)

	if _, err := s.runTmux(ctx, args...); err != nil {
		return fmt.Errorf("send keys to pane %q: %w", paneID, err)
	}

	return nil
}

// CapturePaneOutput captures the last N lines from a pane.
func (s *Session) CapturePaneOutput(ctx context.Context, paneID string, lines int) (string, error) {
	if s == nil {
		return "", fmt.Errorf("session is nil")
	}
	if paneID == "" {
		return "", fmt.Errorf("pane id is empty")
	}
	if lines <= 0 {
		return "", fmt.Errorf("lines must be greater than zero")
	}

	output, err := s.runTmux(ctx, "capture-pane", "-t", paneID, "-p", "-S", "-"+strconv.Itoa(lines))
	if err != nil {
		return "", fmt.Errorf("capture pane output for %q: %w", paneID, err)
	}

	return string(output), nil
}

// KillPane destroys a pane.
func (s *Session) KillPane(ctx context.Context, paneID string) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if paneID == "" {
		return fmt.Errorf("pane id is empty")
	}

	if _, err := s.runTmux(ctx, "kill-pane", "-t", paneID); err != nil {
		return fmt.Errorf("kill pane %q: %w", paneID, err)
	}

	return nil
}

// IsPaneDead reports whether a pane is marked dead by tmux.
func (s *Session) IsPaneDead(ctx context.Context, paneID string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("session is nil")
	}
	if paneID == "" {
		return false, fmt.Errorf("pane id is empty")
	}

	output, err := s.runTmux(ctx, "list-panes", "-t", paneID, "-F", "#{pane_dead}")
	if err != nil {
		return false, fmt.Errorf("check pane %q dead status: %w", paneID, err)
	}

	status := strings.TrimSpace(string(output))
	if status == "" {
		return false, fmt.Errorf("check pane %q dead status: empty response", paneID)
	}

	return strings.HasPrefix(status, "1"), nil
}

// IsTmuxAvailable checks whether the tmux binary can be executed.
func IsTmuxAvailable(ctx context.Context, runner CommandRunner) bool {
	if runner == nil {
		runner = DefaultRunner{}
	}

	_, err := runTmux(ctx, runner, "-V")
	return err == nil
}

func (s *Session) runTmux(ctx context.Context, args ...string) ([]byte, error) {
	if s.runner == nil {
		s.runner = DefaultRunner{}
	}

	return runTmux(ctx, s.runner, args...)
}

func runTmux(ctx context.Context, runner CommandRunner, args ...string) ([]byte, error) {
	if runner == nil {
		return nil, fmt.Errorf("command runner is nil")
	}

	output, err := runner.Run(ctx, "tmux", args...)
	if err == nil {
		return output, nil
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return output, fmt.Errorf("tmux executable not found: %w", err)
	}

	return output, err
}
