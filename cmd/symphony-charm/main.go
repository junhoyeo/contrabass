package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	symphonycharm "github.com/junhoyeo/symphony-charm"
	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/hub"
	"github.com/junhoyeo/symphony-charm/internal/logging"
	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/tui"
	"github.com/junhoyeo/symphony-charm/internal/web"
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
		port     int
	)

	cmd := &cobra.Command{
		Use:   "symphony-charm",
		Short: "Orchestrate coding agents with a Charm TUI dashboard",
		Long: `Symphony-Charm is a Go reimplementation of OpenAI's Symphony.
It orchestrates coding agents against an issue tracker and visualises
progress in a terminal UI built with the Charm stack.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfgPath, noTUI, logFile, logLevel, dryRun, port)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "path to WORKFLOW.md file (required)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "headless mode — skip TUI, log events to stdout")
	cmd.Flags().StringVar(&logFile, "log-file", "symphony-charm.log", "log output path")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level (debug/info/warn/error)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "exit after first poll cycle")
	cmd.Flags().IntVar(&port, "port", 0, "web dashboard port (0 = disabled)")

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
func run(cfgPath string, noTUI bool, logFile, logLevel string, dryRun bool, port int) error {
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

	if dryRun {
		return runDryRun(ctx, orch)
	}

	var h *hub.Hub
	if port > 0 {
		h = hub.NewHub(orch.Events())
		go h.Run(ctx)

		dashboardFS, err := fs.Sub(symphonycharm.DashboardDistFS, "packages/dashboard/dist")
		if err != nil {
			return fmt.Errorf("sub dashboard dist fs: %w", err)
		}

		srv := web.NewServer(fmt.Sprintf("localhost:%d", port), orch, h, dashboardFS)
		go func() {
			if err := srv.Start(ctx); err != nil {
				logger.Error("web server error", "err", err)
			}
		}()

		fmt.Fprintf(os.Stderr, "Web dashboard available at http://localhost:%d\n", port)
	}

	// 10. Select run mode
	if noTUI {
		return runHeadless(ctx, orch, logger, h)
	}
	return runTUI(ctx, orch, h)
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
func runHeadless(ctx context.Context, orch *orchestrator.Orchestrator, logger *log.Logger, h *hub.Hub) error {
	events := orch.Events()
	if h != nil {
		_, subscribedEvents := h.Subscribe()
		events = subscribedEvents
	}

	go func() {
		for event := range events {
			logger.Info("event",
				"type", event.Type.String(),
				"issue_id", event.IssueID,
			)
		}
	}()

	return orch.Run(ctx)
}

// runTUI starts the orchestrator and renders the Charm TUI.
func runTUI(ctx context.Context, orch *orchestrator.Orchestrator, h *hub.Hub) error {
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	model := tui.NewModel()
	p := tea.NewProgram(model)

	events := orch.Events()
	if h != nil {
		_, subscribedEvents := h.Subscribe()
		events = subscribedEvents
	}

	tui.StartEventBridge(p, events)

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
