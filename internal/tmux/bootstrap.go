package tmux

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultBootstrapStartupDelay = 2 * time.Second

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type BootstrapConfig struct {
	WorkerID        string
	TeamName        string
	WorkDir         string
	CLICommand      string
	CLIArgs         []string
	BootstrapPrompt string
	Env             map[string]string
}

type WorkerBootstrap struct {
	session      *Session
	config       BootstrapConfig
	startupDelay time.Duration
}

func NewWorkerBootstrap(session *Session, config BootstrapConfig) *WorkerBootstrap {
	return &WorkerBootstrap{
		session:      session,
		config:       config,
		startupDelay: defaultBootstrapStartupDelay,
	}
}

func (w *WorkerBootstrap) Bootstrap(ctx context.Context) (paneID string, err error) {
	if w == nil {
		return "", fmt.Errorf("worker bootstrap is nil")
	}
	if err := w.validateConfig(); err != nil {
		return "", err
	}

	createdPaneID, err := w.session.NewWindow(ctx, strings.TrimSpace(w.config.WorkerID))
	if err != nil {
		return "", fmt.Errorf("create worker window %q: %w", w.config.WorkerID, err)
	}

	cleanup := true
	defer func() {
		if !cleanup {
			return
		}
		if killErr := w.session.KillPane(context.Background(), createdPaneID); killErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup pane %q: %w", createdPaneID, killErr))
		}
	}()

	if err := w.exportEnv(ctx, createdPaneID); err != nil {
		return "", err
	}

	if err := w.session.SendKeys(ctx, createdPaneID, "cd "+shellQuote(w.config.WorkDir), "C-m"); err != nil {
		return "", fmt.Errorf("change worker %q directory to %q: %w", w.config.WorkerID, w.config.WorkDir, err)
	}

	// Build the full CLI command with properly quoted arguments
	cliParts := []string{shellQuote(strings.TrimSpace(w.config.CLICommand))}
	for _, arg := range w.config.CLIArgs {
		cliParts = append(cliParts, shellQuote(arg))
	}
	fullCLICmd := strings.Join(cliParts, " ")

	if err := w.session.SendKeys(ctx, createdPaneID, fullCLICmd, "C-m"); err != nil {
		return "", fmt.Errorf("launch worker %q cli command: %w", w.config.WorkerID, err)
	}

	if err := w.waitForStartup(ctx); err != nil {
		return "", err
	}

	cleanup = false
	return createdPaneID, nil
}

func (w *WorkerBootstrap) InjectPrompt(ctx context.Context, paneID string, prompt string) error {
	if w == nil {
		return fmt.Errorf("worker bootstrap is nil")
	}
	if w.session == nil {
		return fmt.Errorf("session is nil")
	}
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("pane id is empty")
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("prompt is empty")
	}

	escapedPrompt := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\n", "\\n")
	if err := w.session.SendKeys(ctx, paneID, escapedPrompt, "C-m"); err != nil {
		return fmt.Errorf("inject prompt into pane %q: %w", paneID, err)
	}

	return nil
}

func (w *WorkerBootstrap) IsWorkerAlive(ctx context.Context, paneID string) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("worker bootstrap is nil")
	}
	if w.session == nil {
		return false, fmt.Errorf("session is nil")
	}

	dead, err := w.session.IsPaneDead(ctx, paneID)
	if err != nil {
		return false, err
	}

	return !dead, nil
}

func (w *WorkerBootstrap) validateConfig() error {
	if w.session == nil {
		return fmt.Errorf("session is nil")
	}
	if strings.TrimSpace(w.config.WorkerID) == "" {
		return fmt.Errorf("worker id is empty")
	}
	if strings.TrimSpace(w.config.TeamName) == "" {
		return fmt.Errorf("team name is empty")
	}
	if strings.TrimSpace(w.config.WorkDir) == "" {
		return fmt.Errorf("work dir is empty")
	}
	if strings.TrimSpace(w.config.CLICommand) == "" {
		return fmt.Errorf("cli command is empty")
	}

	for key := range w.config.Env {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid environment variable key %q", key)
		}
	}

	return nil
}

func (w *WorkerBootstrap) exportEnv(ctx context.Context, paneID string) error {
	if len(w.config.Env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(w.config.Env))
	for key := range w.config.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := w.config.Env[key]
		cmd := fmt.Sprintf("export %s=%s", key, shellQuote(value))
		if err := w.session.SendKeys(ctx, paneID, cmd, "C-m"); err != nil {
			return fmt.Errorf("set environment variable %q for worker %q: %w", key, w.config.WorkerID, err)
		}
	}

	return nil
}

func (w *WorkerBootstrap) waitForStartup(ctx context.Context) error {
	delay := w.startupDelay
	if delay <= 0 {
		delay = defaultBootstrapStartupDelay
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
