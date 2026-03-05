package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/logging"
	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/tui"
	"github.com/junhoyeo/symphony-charm/internal/workspace"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCmd builds the Cobra root command with all CLI flags.
func newRootCmd() *cobra.Command {
	var (
		cfgPath  string
		noTUI    bool
		logFile  string
		logLevel string
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "symphony-charm",
		Short: "Orchestrate coding agents with a Charm TUI dashboard",
		Long: `Symphony-Charm is a Go reimplementation of OpenAI's Symphony.
It orchestrates coding agents against an issue tracker and visualises
progress in a terminal UI built with the Charm stack.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfgPath, noTUI, logFile, logLevel, dryRun)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "path to WORKFLOW.md file (required)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "headless mode — skip TUI, log events to stdout")
	cmd.Flags().StringVar(&logFile, "log-file", "symphony-charm.log", "log output path")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level (debug/info/warn/error)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "exit after first poll cycle")

	_ = cmd.MarkFlagRequired("config")

	return cmd
}

// parseLogLevel converts a string log level to the charmbracelet/log Level.
func parseLogLevel(s string) log.Level {
	switch strings.ToLower(s) {
	case "debug":
		return log.DebugLevel
	case "warn":
		return log.WarnLevel
	case "error":
		return log.ErrorLevel
	default:
		return log.InfoLevel
	}
}

// run is the main entry point wired into the root command's RunE.
func run(cfgPath string, noTUI bool, logFile, logLevel string, dryRun bool) error {
	// 1. Parse and validate workflow config
	cfg, err := config.ParseWorkflow(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	// 2. Create logger
	logger := logging.NewLogger(logging.LogOptions{
		Level:  parseLogLevel(logLevel),
		Output: logFile,
		Prefix: "symphony-charm",
	})

	// 3. Create config watcher (live reload via fsnotify)
	watcher, err := config.NewWatcher(cfgPath)
	if err != nil {
		return fmt.Errorf("creating config watcher: %w", err)
	}
	defer watcher.Stop()

	// 4. Signal-aware context — cancelled on SIGINT/SIGTERM or when run returns
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 5. Start watching config file for changes
	go func() {
		if watchErr := watcher.Watch(ctx); watchErr != nil {
			logger.Error("config watcher failed", "err", watchErr)
		}
	}()

	// 6. Create tracker (Linear client)
	linearClient := tracker.NewLinearClient(tracker.LinearConfig{
		APIKey:      os.Getenv("LINEAR_API_KEY"),
		ProjectSlug: projectSlug(cfg),
	})

	// 7. Create workspace manager (uses cwd as repo root)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	workspaceMgr := workspace.NewManager(repoPath)

	// 8. Create agent runner
	codexBin := os.Getenv("CODEX_BINARY")
	if codexBin == "" {
		codexBin = "codex"
	}
	agentRunner := agent.NewCodexRunner(codexBin, 30*time.Second)

	// 9. Create orchestrator
	orch := orchestrator.NewOrchestrator(linearClient, workspaceMgr, agentRunner, watcher, logger)

	// 10. Select run mode
	if dryRun {
		return runDryRun(ctx, orch)
	}
	if noTUI {
		return runHeadless(ctx, orch, logger)
	}
	return runTUI(ctx, orch)
}

// runDryRun starts the orchestrator and exits after the first emitted event.
func runDryRun(ctx context.Context, orch *orchestrator.Orchestrator) error {
	dryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		if _, ok := <-orch.Events(); ok {
			cancel()
		}
	}()

	return orch.Run(dryCtx)
}

// runHeadless runs the orchestrator without TUI, logging events to the logger.
func runHeadless(ctx context.Context, orch *orchestrator.Orchestrator, logger *log.Logger) error {
	go func() {
		for event := range orch.Events() {
			logger.Info("event",
				"type", event.Type.String(),
				"issue_id", event.IssueID,
			)
		}
	}()

	return orch.Run(ctx)
}

// runTUI starts the orchestrator and renders the Charm TUI.
func runTUI(ctx context.Context, orch *orchestrator.Orchestrator) error {
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	model := tui.NewModel()
	p := tea.NewProgram(withViewportProgramOptions(model))

	tui.StartEventBridge(p, orch.Events())

	orchDone := make(chan error, 1)
	go func() {
		orchDone <- orch.Run(tuiCtx)
	}()

	_, tuiErr := p.Run()

	// TUI exited — cancel orchestrator context and wait for graceful shutdown
	tuiCancel()
	select {
	case <-orchDone:
	case <-time.After(6 * time.Second):
	}

	return tuiErr
}

type viewportProgramModel struct {
	model tea.Model
}

func withViewportProgramOptions(model tea.Model) tea.Model {
	return viewportProgramModel{model: model}
}

func (m viewportProgramModel) Init() tea.Cmd {
	return m.model.Init()
}

func (m viewportProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.model.Update(msg)
	m.model = next
	return m, cmd
}

func (m viewportProgramModel) View() tea.View {
	v := m.model.View()
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// projectSlug extracts the Linear project slug from env or config.
func projectSlug(cfg *config.WorkflowConfig) string {
	if envSlug := os.Getenv("LINEAR_PROJECT_SLUG"); envSlug != "" {
		return envSlug
	}
	url, err := cfg.ProjectURL()
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
