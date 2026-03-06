package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/tui"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

var (
	runTUIOrchestrator = func(ctx context.Context, orch *orchestrator.Orchestrator) error {
		return orch.Run(ctx)
	}
	runGracefulShutdown = orchestrator.GracefulShutdown
	runTUIProgram       = func(p *tea.Program) (tea.Model, error) {
		return p.Run()
	}
	startTUIEventBridge = func(ctx context.Context, p *tea.Program, events <-chan orchestrator.OrchestratorEvent) {
		tui.StartEventBridge(ctx, p, events)
	}
	runTUIShutdownTimeout = 6 * time.Second
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
		Use:   "contrabass",
		Short: "Orchestrate coding agents with a Charm TUI dashboard",
		Long: `Contrabass is a Go reimplementation of OpenAI's Symphony.
It orchestrates coding agents against an issue tracker and visualises
progress in a terminal UI built with the Charm stack.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfgPath, noTUI, logFile, logLevel, dryRun)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "path to WORKFLOW.md file (required)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "headless mode — skip TUI, log events to stdout")
	cmd.Flags().StringVar(&logFile, "log-file", "contrabass.log", "log output path")
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
		Prefix: "contrabass",
	})

	// 3. Create config watcher (live reload via fsnotify)
	watcher, err := config.NewWatcher(cfgPath)
	if err != nil {
		return fmt.Errorf("creating config watcher: %w", err)
	}
	defer watcher.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 5. Start watching config file for changes
	go func() {
		if watchErr := watcher.Watch(ctx); watchErr != nil {
			logger.Error("config watcher failed", "err", watchErr)
		}
	}()

	// 6. Create tracker
	var trackerClient tracker.Tracker
	switch cfg.TrackerType() {
	case "linear":
		assigneeID := trackerAssigneeID(cfg)
		linearClient := tracker.NewLinearClient(tracker.LinearConfig{
			APIKey:      os.Getenv("LINEAR_API_KEY"),
			ProjectSlug: projectSlug(cfg),
			AssigneeID:  assigneeID,
		})
		if assigneeID == "" {
			logger.Info("no assignee configured, resolving from API token...")
			viewerID, viewerErr := linearClient.FetchViewerID(ctx)
			if viewerErr != nil {
				logger.Warn("could not auto-resolve assignee from API token", "err", viewerErr)
				logger.Warn("set tracker.assignee_id or LINEAR_ASSIGNEE to claim issues")
			} else {
				linearClient.SetAssigneeID(viewerID)
				logger.Info("auto-resolved assignee from API token", "id", viewerID)
			}
		}
		trackerClient = linearClient
	case "github":
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = cfg.GitHubToken()
		}
		owner := os.Getenv("GITHUB_OWNER")
		if owner == "" {
			owner = cfg.GitHubOwner()
		}
		repo := os.Getenv("GITHUB_REPO")
		if repo == "" {
			repo = cfg.GitHubRepo()
		}
		trackerClient = tracker.NewGitHubClient(tracker.GitHubConfig{
			APIToken: token,
			Owner:    owner,
			Repo:     repo,
			Labels:   cfg.GitHubLabels(),
			Assignee: cfg.GitHubAssignee(),
			Endpoint: cfg.GitHubEndpoint(),
		})
	default:
		return fmt.Errorf("unknown tracker type: %q (supported: linear, github)", cfg.TrackerType())
	}

	// 7. Create workspace manager (uses cwd as repo root)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	workspaceMgr := workspace.NewManager(repoPath)

	// 8. Create agent runner
	var agentRunner agent.AgentRunner
	switch cfg.AgentType() {
	case "codex":
		codexBin := os.Getenv("CODEX_BINARY")
		if codexBin == "" {
			codexBin = cfg.CodexBinaryPath()
		}
		agentRunner = agent.NewCodexRunner(codexBin, 30*time.Second)
	case "opencode":
		opencodeBin := os.Getenv("OPENCODE_BINARY")
		if opencodeBin == "" {
			opencodeBin = cfg.OpenCodeBinaryPath()
		}
		port := cfg.OpenCodePort()
		password := os.Getenv("OPENCODE_SERVER_PASSWORD")
		if password == "" {
			password = cfg.OpenCodePassword()
		}
		username := os.Getenv("OPENCODE_SERVER_USERNAME")
		if username == "" {
			username = cfg.OpenCodeUsername()
		}
		agentRunner = agent.NewOpenCodeRunner(opencodeBin, port, password, username, 30*time.Second)
	default:
		return fmt.Errorf("unknown agent type: %q (supported: codex, opencode)", cfg.AgentType())
	}

	defer agentRunner.Close()

	// 9. Create orchestrator
	orch := orchestrator.NewOrchestrator(trackerClient, workspaceMgr, agentRunner, watcher, logger)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChan)
	startSignalShutdownHook(ctx, signalChan, cancel, orch, logger)

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
// If no event arrives within the timeout, it logs a warning and returns nil.
func runDryRun(ctx context.Context, orch *orchestrator.Orchestrator) error {
	dryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	go func() {
		if _, ok := <-orch.Events(); ok {
			cancel()
		}
	}()

	err := orch.Run(dryCtx)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		log.Warn("dry-run timeout: no events received within 60s")
		return nil
	}
	return err
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

func startSignalShutdownHook(
	ctx context.Context,
	signalChan <-chan os.Signal,
	cancel context.CancelFunc,
	orch *orchestrator.Orchestrator,
	logger *log.Logger,
) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-signalChan:
			if shutdownErr := runGracefulShutdown(cancel, orch, orchestrator.DefaultShutdownConfig(), logger); shutdownErr != nil {
				logger.Error("graceful shutdown failed", "err", shutdownErr)
			}
		}
	}()
}

// runTUI starts the orchestrator and renders the Charm TUI.
func runTUI(ctx context.Context, orch *orchestrator.Orchestrator) error {
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	model := tui.NewModel()
	p := tea.NewProgram(withViewportProgramOptions(model))

	startTUIEventBridge(tuiCtx, p, orch.Events())

	orchDone := make(chan error, 1)
	orchestratorRunner := runTUIOrchestrator
	go func() {
		defer func() {
			if r := recover(); r != nil {
				orchDone <- fmt.Errorf("orchestrator panic: %v", r)
			}
		}()
		orchDone <- orchestratorRunner(tuiCtx, orch)
	}()

	_, tuiErr := runTUIProgram(p)
	// Clean up native Kitty image AFTER alt-screen exit so the delete
	// command targets the main screen where the image persists.
	tui.CleanupNativeImage()

	// TUI exited — cancel orchestrator context and wait for graceful shutdown
	tuiCancel()
	select {
	case orchErr := <-orchDone:
		if orchErr != nil {
			if tuiErr != nil {
				return fmt.Errorf("orchestrator failed: %w (tui error: %v)", orchErr, tuiErr)
			}
			return orchErr
		}
	case <-time.After(runTUIShutdownTimeout):
		if tuiErr != nil {
			return fmt.Errorf("timed out waiting for orchestrator shutdown: %w", tuiErr)
		}
		return errors.New("timed out waiting for orchestrator shutdown")
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

// trackerAssigneeID extracts assignee from config with env fallback.
func trackerAssigneeID(cfg *config.WorkflowConfig) string {
	if cfgAssignee := cfg.TrackerAssigneeID(); cfgAssignee != "" {
		return cfgAssignee
	}
	return os.Getenv("LINEAR_ASSIGNEE")
}
