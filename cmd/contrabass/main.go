package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	contrabass "github.com/junhoyeo/contrabass"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/hub"
	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/tui"
	"github.com/junhoyeo/contrabass/internal/update"
	"github.com/junhoyeo/contrabass/internal/web"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

// Build-time variables injected via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
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
	runRootTeamExecution = func(
		ctx context.Context,
		cfgPath string,
		watcher *config.Watcher,
		logger *log.Logger,
		noTUI bool,
		dryRun bool,
		port int,
	) error {
		return runTeamExecutionApp(ctx, cfgPath, watcher, logger, noTUI, dryRun, port)
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
		port     int
	)

	var updateResult update.Result

	cmd := &cobra.Command{
		Use:     "contrabass",
		Short:   "Orchestrate coding agents with a Charm TUI dashboard",
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
		Long: `Contrabass is a Go reimplementation of OpenAI's Symphony.
It orchestrates coding agents against an issue tracker and visualises
progress in a terminal UI built with the Charm stack.`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			updateResult = update.Check(context.Background(), version)
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if msg := update.FormatNotification(updateResult); msg != "" {
				fmt.Fprint(os.Stderr, msg)
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfgPath, noTUI, logFile, logLevel, dryRun, port)
		},
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "path to WORKFLOW.md file (required)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "headless mode — skip TUI, log events to stdout")
	cmd.Flags().StringVar(&logFile, "log-file", "contrabass.log", "log output path")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level (debug/info/warn/error)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "exit after first poll cycle")
	cmd.Flags().IntVar(&port, "port", 0, "web dashboard port (0 = disabled)")

	_ = cmd.MarkFlagRequired("config")

	cmd.AddCommand(teamCmd, boardCmd, newConfigCmd())

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

// newSessionID returns an 8-character hex token uniquely identifying this run,
// so concurrent contrabass instances do not interleave entries into a shared log
// file. Falls back to a nanosecond timestamp if crypto/rand is unavailable.
func newSessionID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

// run is the main entry point wired into the root command's RunE.
func run(cfgPath string, noTUI bool, logFile, logLevel string, dryRun bool, port int) error {
	// 1. Parse and validate workflow config
	cfg, err := config.ParseWorkflow(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	// 2. Create logger
	session := newSessionID()
	resolvedLogFile := logging.ResolveLogPath(logFile, session)
	logger := logging.NewLogger(logging.LogOptions{
		Level:   parseLogLevel(logLevel),
		Output:  logFile,
		Prefix:  "contrabass",
		Session: session,
	})
	logTarget := resolvedLogFile
	if logTarget == "" {
		logTarget = "stderr"
	}
	fmt.Fprintf(os.Stderr, "contrabass session=%s log=%s\n", session, logTarget)

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

	switch cfg.TeamExecutionMode() {
	case config.TeamExecutionModeTeam:
		// This branch returns before the single-agent signal hook below is
		// installed, and runTeamExecutionLoop disables the team-level handler,
		// so without this wrapper SIGINT/SIGTERM would hit the Go default
		// disposition: the process dies before runner.Close and
		// boardSyncer.Finalize run, orphaning tmux panes and leaving board
		// issues claimed. Cancelling ctx routes shutdown through the normal
		// exit path instead.
		teamCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		return runRootTeamExecution(teamCtx, cfgPath, watcher, logger, noTUI, dryRun, port)
	case config.TeamExecutionModeSingle:
		// Continue into the original single-agent orchestrator path.
	default:
		return fmt.Errorf(
			"unknown team.execution_mode: %q (supported: auto, team, single)",
			cfg.Team.ExecutionMode,
		)
	}

	// 6. Create tracker
	var trackerClient tracker.Tracker
	switch cfg.TrackerType() {
	case "linear":
		assigneeID := trackerAssigneeID(cfg)
		linearClient, linearErr := tracker.NewLinearClient(tracker.LinearConfig{
			APIKey:      os.Getenv("LINEAR_API_KEY"),
			ProjectSlug: projectSlug(cfg),
			AssigneeID:  assigneeID,
		})
		if linearErr != nil {
			return fmt.Errorf("creating linear tracker client: %w", linearErr)
		}
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
		githubClient, githubErr := tracker.NewGitHubClient(tracker.GitHubConfig{
			APIToken: token,
			Owner:    owner,
			Repo:     repo,
			Labels:   cfg.GitHubLabels(),
			Assignee: cfg.GitHubAssignee(),
			Endpoint: cfg.GitHubEndpoint(),
		})
		if githubErr != nil {
			return fmt.Errorf("creating github tracker client: %w", githubErr)
		}
		trackerClient = githubClient
	case "internal", "local":
		trackerClient = tracker.NewLocalTracker(tracker.LocalConfig{
			BoardDir:    cfg.LocalBoardDir(),
			IssuePrefix: cfg.LocalIssuePrefix(),
			Actor:       cfg.GitHubAssignee(),
		})
	default:
		return fmt.Errorf("unknown tracker type: %q (supported: internal, local, linear, github)", cfg.TrackerType())
	}

	// 7. Create workspace manager (uses cwd as repo root)
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	workspaceMgr := workspace.NewManager(repoPath)

	// 8. Create agent runner (reuses createRunner from team.go)
	agentRunner, err := createRunner(cfg, "orchestrator", nil)
	if err != nil {
		return fmt.Errorf("creating agent runner: %w", err)
	}

	defer agentRunner.Close()

	// 9. Create orchestrator
	orch := orchestrator.NewOrchestrator(trackerClient, workspaceMgr, agentRunner, watcher, logger)
	orch.SetBuildInfo(orchestrator.BuildInfo{Version: version, Commit: commit, Date: date})
	timelineStore := timeline.NewStore(cfg.WorkflowTimelineDir())
	orch.SetWorkflowTimeline(timelineStore, cfg.LinearSyncCommentsEnabled())
	var linearSyncer *timeline.LinearSyncer
	if cfg.LinearSyncCommentsEnabled() {
		if cfg.TrackerType() == "linear" {
			writer, ok := trackerClient.(tracker.LinearCommentWriter)
			if !ok {
				return fmt.Errorf("linear comment sync enabled but tracker does not support Linear comment writer")
			}
			linearSyncer = timeline.NewLinearSyncer(timelineStore, writer, timeline.LinearSyncerConfig{
				Mode:               cfg.LinearSyncCommentsMode(),
				AllowReplyFallback: !cfg.LinearSyncCommentsModeExplicit(),
				QueueSize:          cfg.LinearSyncCommentsQueueSize(),
				PollInterval:       time.Duration(cfg.LinearSyncCommentsPollIntervalMs()) * time.Millisecond,
				Logger:             logger,
			})
			go func() {
				if err := linearSyncer.Run(ctx); err != nil {
					logger.Warn("linear comment syncer stopped", "err", err)
				}
			}()
		} else {
			logger.Warn("linear comment sync enabled for non-linear tracker; legacy comments remain active",
				"tracker_type", cfg.TrackerType())
		}
	}
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChan)
	startSignalShutdownHook(ctx, signalChan, cancel, orch, logger)

	if dryRun {
		return runDryRun(ctx, orch)
	}

	var h *hub.Hub[web.WebEvent]
	if port > 0 {
		webEvents := make(chan web.WebEvent, 256)
		h = hub.NewHub(webEvents)
		go h.Run(ctx)

		go forwardOrchestratorEvents(orch.Events(), webEvents)

		var dashboardFS fs.FS
		if _, statErr := fs.Stat(contrabass.DashboardDistFS, "packages/dashboard/dist"); statErr == nil {
			dashboardFS, err = fs.Sub(contrabass.DashboardDistFS, "packages/dashboard/dist")
			if err != nil {
				return fmt.Errorf("sub dashboard dist fs: %w", err)
			}
		}

		srv := web.NewServer(fmt.Sprintf("localhost:%d", port), orch, h, dashboardFS)
		// Board mutations made over HTTP must flow back into the hub so other
		// dashboard tabs and stream subscribers see them live.
		srv.SetEventSink(webEvents)
		srv.SetAgentStopper(orch)
		if boardProvider, ok := trackerClient.(web.BoardProvider); ok {
			srv.SetBoardProvider(boardProvider)
		}
		if detailProvider, ok := trackerClient.(tracker.IssueDetailProvider); ok {
			srv.SetIssueDetailProvider(detailProvider)
		}
		srv.SetTimelineProvider(timelineStore)
		listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			return fmt.Errorf("listen web dashboard: %w", err)
		}
		go func() {
			if err := srv.Serve(ctx, listener); err != nil {
				logger.Error("web server error", "err", err)
			}
		}()

		if dashboardFS != nil {
			fmt.Fprintf(os.Stderr, "Web dashboard available at http://localhost:%d\n", port)
		} else {
			fmt.Fprintf(os.Stderr, "Web API available at http://localhost:%d (dashboard assets not embedded; rebuild with the dashboard_dist tag)\n", port)
		}
	}

	// 10. Select run mode
	if noTUI {
		return runHeadless(ctx, orch, logger, h, os.Stdout)
	}
	return runTUI(ctx, orch, h)
}

// forwardOrchestratorEvents copies orchestrator events into the hub source
// channel. It must NOT close webEvents: HTTP handlers publish board mutations
// into the same channel (Server.SetEventSink), and in-flight requests can
// outlive the orchestrator during the server's graceful shutdown window —
// closing here would make publishEvent panic with send on closed channel.
// The hub shuts down via ctx instead.
func forwardOrchestratorEvents(orchEvents <-chan orchestrator.OrchestratorEvent, webEvents chan<- web.WebEvent) {
	for orchEvent := range orchEvents {
		webEvents <- web.NewOrchestratorWebEvent(orchEvent)
	}
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
	// Orchestrator.Run returns nil when its context is done, so the timeout
	// must be detected on dryCtx itself rather than on the returned error.
	if errors.Is(dryCtx.Err(), context.DeadlineExceeded) {
		log.Warn("dry-run timeout: no events received within 60s")
		return nil
	}
	return err
}

// runHeadless runs the orchestrator without TUI, streaming events to out
// (stdout in production, as promised by the --no-tui help text) in addition
// to the file logger.
func runHeadless(
	ctx context.Context,
	orch *orchestrator.Orchestrator,
	logger *log.Logger,
	h *hub.Hub[web.WebEvent],
	out io.Writer,
) error {
	if h != nil {
		subID, subscribedEvents := h.Subscribe()
		defer h.Unsubscribe(subID)
		go streamHeadlessWebEvents(out, logger, subscribedEvents)
	} else {
		go streamHeadlessOrchestratorEvents(out, logger, orch.Events())
	}

	return orch.Run(ctx)
}

func streamHeadlessWebEvents(out io.Writer, logger *log.Logger, events <-chan web.WebEvent) {
	for webEvt := range events {
		_, _ = fmt.Fprintf(out, "event kind=%s type=%s\n", string(webEvt.Kind), webEvt.Type)
		logger.Info("event",
			"kind", string(webEvt.Kind),
			"type", webEvt.Type,
		)
	}
}

func streamHeadlessOrchestratorEvents(
	out io.Writer,
	logger *log.Logger,
	events <-chan orchestrator.OrchestratorEvent,
) {
	for event := range events {
		_, _ = fmt.Fprintf(out, "event type=%s issue_id=%s\n", event.Type.String(), event.IssueID)
		logger.Info("event",
			"type", event.Type.String(),
			"issue_id", event.IssueID,
		)
	}
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
			if shutdownErr := runGracefulShutdown(cancel, orch, orch.ShutdownConfig(), logger); shutdownErr != nil {
				logger.Error("graceful shutdown failed", "err", shutdownErr)
			}
		}
	}()
}

// runTUI starts the orchestrator and renders the Charm TUI.
// When a hub is provided (web dashboard active), the TUI subscribes to the hub
// instead of reading orch.Events() directly — otherwise both consumers would
// compete for the same channel and randomly split events.
func runTUI(
	ctx context.Context,
	orch *orchestrator.Orchestrator,
	h *hub.Hub[web.WebEvent],
) error {
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	model := tui.NewModel()
	p := tea.NewProgram(withViewportProgramOptions(model))

	if h != nil {
		subID, webEvents := h.Subscribe()
		defer h.Unsubscribe(subID)
		orchEvents := make(chan orchestrator.OrchestratorEvent, 256)
		go func() {
			defer close(orchEvents)
			for {
				select {
				case <-tuiCtx.Done():
					return
				case we, ok := <-webEvents:
					if !ok {
						return
					}
					if we.Kind == web.WebEventOrchestrator {
						if oe, ok := we.Payload.(orchestrator.OrchestratorEvent); ok {
							orchEvents <- oe
						}
					}
				}
			}
		}()
		startTUIEventBridge(tuiCtx, p, orchEvents)
	} else {
		startTUIEventBridge(tuiCtx, p, orch.Events())
	}

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
