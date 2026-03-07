package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/team"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Manage coordinated agent teams",
	Long:  "Manage coordinated agent teams executing staged pipelines",
}

var teamRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a team with the staged pipeline",
	RunE:  runTeam,
}

var teamStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show team status",
	RunE:  showTeamStatus,
}

var teamCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel a running team",
	RunE:  cancelTeam,
}

type teamRunOptions struct {
	ConfigPath string
	TeamName   string
	TasksPath  string
	IssueID    string
	MaxWorkers int
}

type teamRunHooks struct {
	ParentContext        context.Context
	EventHandlers        []teamEventHandler
	DisableSignalHandler bool
}

func init() {
	// teamRunCmd flags
	teamRunCmd.Flags().StringP("config", "c", "", "path to WORKFLOW.md file (required)")
	teamRunCmd.Flags().StringP("name", "n", "", "team name (required unless --issue is set)")
	teamRunCmd.Flags().StringP("tasks", "t", "", "path to tasks JSON file (required unless --issue is set)")
	teamRunCmd.Flags().String("issue", "", "internal board issue ID to hydrate into a team run")
	teamRunCmd.Flags().IntP("max-workers", "w", 0, "override max workers from config")

	_ = teamRunCmd.MarkFlagRequired("config")

	// teamStatusCmd flags
	teamStatusCmd.Flags().StringP("config", "c", "", "path to WORKFLOW.md file (required)")
	teamStatusCmd.Flags().StringP("name", "n", "", "team name (required)")

	_ = teamStatusCmd.MarkFlagRequired("config")
	_ = teamStatusCmd.MarkFlagRequired("name")

	// teamCancelCmd flags
	teamCancelCmd.Flags().StringP("config", "c", "", "path to WORKFLOW.md file (required)")
	teamCancelCmd.Flags().StringP("name", "n", "", "team name (required)")

	_ = teamCancelCmd.MarkFlagRequired("config")
	_ = teamCancelCmd.MarkFlagRequired("name")

	// Add subcommands to teamCmd
	teamCmd.AddCommand(teamRunCmd)
	teamCmd.AddCommand(teamStatusCmd)
	teamCmd.AddCommand(teamCancelCmd)
}

func logTeamEvents(ctx context.Context, logger *slog.Logger, events <-chan types.TeamEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			logger.Info("team event",
				"type", event.Type,
				"team", event.TeamName,
				"data", event.Data,
			)
		}
	}
}

// createRunner creates an AgentRunner based on the workflow config.
func createRunner(cfg *config.WorkflowConfig) (agent.AgentRunner, error) {
	switch cfg.AgentType() {
	case "codex":
		codexBin := os.Getenv("CODEX_BINARY")
		if codexBin == "" {
			codexBin = cfg.CodexBinaryPath()
		}
		return agent.NewCodexRunner(codexBin, 30*time.Second), nil
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
		return agent.NewOpenCodeRunner(opencodeBin, port, password, username, 30*time.Second), nil
	case "oh-my-opencode":
		return agent.NewOhMyOpenCodeRunner(cfg, 30*time.Second)
	default:
		return nil, fmt.Errorf("unknown agent type: %q", cfg.AgentType())
	}
}

// runTeam executes the team run subcommand.
func runTeam(cmd *cobra.Command, args []string) error {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("getting config flag: %w", err)
	}

	teamName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("getting name flag: %w", err)
	}

	tasksPath, err := cmd.Flags().GetString("tasks")
	if err != nil {
		return fmt.Errorf("getting tasks flag: %w", err)
	}

	issueID, err := cmd.Flags().GetString("issue")
	if err != nil {
		return fmt.Errorf("getting issue flag: %w", err)
	}

	maxWorkers, err := cmd.Flags().GetInt("max-workers")
	if err != nil {
		return fmt.Errorf("getting max-workers flag: %w", err)
	}

	return runTeamWithOptions(teamRunOptions{
		ConfigPath: cfgPath,
		TeamName:   teamName,
		TasksPath:  tasksPath,
		IssueID:    issueID,
		MaxWorkers: maxWorkers,
	})
}

func runTeamWithOptions(opts teamRunOptions) error {
	return runTeamWithHooks(opts, teamRunHooks{})
}

func runTeamWithHooks(opts teamRunOptions, hooks teamRunHooks) error {
	switch {
	case opts.TasksPath != "" && opts.IssueID != "":
		return errors.New("provide either --tasks or --issue, not both")
	case opts.TasksPath == "" && opts.IssueID == "":
		return errors.New("either --tasks or --issue is required")
	case opts.TeamName == "" && opts.IssueID == "":
		return errors.New("team name is required when running from --tasks")
	}

	// 1. Parse config
	cfg, err := config.ParseWorkflow(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	// 2. Create workspace manager
	repoPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	workspaceMgr := workspace.NewManager(repoPath)

	// 3. Create agent runner
	runner, err := createRunner(cfg)
	if err != nil {
		return fmt.Errorf("creating agent runner: %w", err)
	}
	defer runner.Close()

	var tasks []types.TeamTask
	var boardSyncer *boardIssueSyncer
	teamName := opts.TeamName
	if opts.IssueID != "" {
		if cfg.TrackerType() != "internal" && cfg.TrackerType() != "local" {
			return fmt.Errorf("team run --issue requires tracker.type internal/local, got %q", cfg.TrackerType())
		}

		localTracker := tracker.NewLocalTracker(tracker.LocalConfig{
			BoardDir:    cfg.LocalBoardDir(),
			IssuePrefix: cfg.LocalIssuePrefix(),
		})

		issue, err := localTracker.GetIssue(context.Background(), opts.IssueID)
		if err != nil {
			return fmt.Errorf("loading internal board issue %q: %w", opts.IssueID, err)
		}

		if teamName == "" {
			teamName = resolveTeamNameForIssue(issue, "")
		}
		localTracker = tracker.NewLocalTracker(tracker.LocalConfig{
			BoardDir:    cfg.LocalBoardDir(),
			IssuePrefix: cfg.LocalIssuePrefix(),
			Actor:       "team:" + teamName,
		})
		childIssues, err := localTracker.ListChildIssues(context.Background(), issue.ID)
		if err != nil {
			return fmt.Errorf("loading child issues for %q: %w", opts.IssueID, err)
		}

		teamPlan := buildBoardTeamPlan(issue, childIssues)
		tasks = teamPlan.Tasks
		boardSyncer = newBoardIssueSyncer(localTracker, opts.IssueID, teamName, teamPlan.TaskIssueIDs)
	} else {
		// 4. Read tasks JSON file
		tasksData, err := os.ReadFile(opts.TasksPath)
		if err != nil {
			return fmt.Errorf("reading tasks file: %w", err)
		}

		if err := json.Unmarshal(tasksData, &tasks); err != nil {
			return fmt.Errorf("unmarshalling tasks: %w", err)
		}
	}

	// 5. Create logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// 6. Create coordinator
	coordinator := team.NewCoordinator(teamName, cfg, runner, workspaceMgr, logger)

	// 7. Create TeamConfig
	teamCfg := types.TeamConfig{
		MaxWorkers:        cfg.TeamMaxWorkers(),
		MaxFixLoops:       cfg.TeamMaxFixLoops(),
		ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
		StateDir:          cfg.TeamStateDir(),
		AgentType:         cfg.AgentType(),
		BoardIssueID:      opts.IssueID,
	}

	// Override max workers if provided — update both teamCfg and the parsed
	// config so that Coordinator.runExecPhase (which reads c.cfg.TeamMaxWorkers())
	// sees the CLI override.
	if opts.MaxWorkers > 0 {
		teamCfg.MaxWorkers = opts.MaxWorkers
		cfg.Team.MaxWorkers = opts.MaxWorkers
	}

	// 8. Initialize team
	if err := coordinator.Initialize(teamCfg); err != nil {
		return fmt.Errorf("initializing team: %w", err)
	}

	// 9. Set up signal handling
	parentCtx := hooks.ParentContext
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	if !hooks.DisableSignalHandler {
		signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signalChan)

		go func() {
			select {
			case <-ctx.Done():
				return
			case <-signalChan:
				logger.Info("received signal, cancelling team")
				cancel()
			}
		}()
	}

	// 10. Start event consumer goroutine
	handlers := append([]teamEventHandler{}, hooks.EventHandlers...)
	if boardSyncer != nil {
		if err := boardSyncer.Start(ctx); err != nil {
			return fmt.Errorf("starting board sync: %w", err)
		}
		handlers = append(handlers, boardSyncer.HandleEvent)
	}
	if len(handlers) == 0 {
		go logTeamEvents(ctx, logger, coordinator.Events)
	} else {
		go consumeTeamEvents(ctx, logger, coordinator.Events, handlers...)
	}

	// 11. Run team
	if err := coordinator.Run(ctx, tasks); err != nil {
		if boardSyncer != nil {
			boardSyncer.Finalize(ctx, err)
		}
		return fmt.Errorf("running team: %w", err)
	}
	if boardSyncer != nil {
		boardSyncer.Finalize(ctx, nil)
	}

	return nil
}

// showTeamStatus displays the current status of a team.
func showTeamStatus(cmd *cobra.Command, args []string) error {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("getting config flag: %w", err)
	}

	teamName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("getting name flag: %w", err)
	}

	// Parse config
	cfg, err := config.ParseWorkflow(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	// Create paths and store
	paths := team.NewPaths(cfg.TeamStateDir())
	store := team.NewStore(paths)

	// Load manifest
	manifest, err := store.LoadManifest(teamName)
	if err != nil {
		return fmt.Errorf("loading team manifest: %w", err)
	}

	// Load phase state
	phaseState, err := store.LoadPhaseState(teamName)
	if err != nil {
		return fmt.Errorf("loading phase state: %w", err)
	}

	// Create task registry to list tasks
	tasks := team.NewTaskRegistry(store, paths, cfg.TeamClaimLeaseSeconds())
	taskList, err := tasks.ListTasks(teamName)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Build status response
	status := team.TeamStatus{
		TeamName:     teamName,
		BoardIssueID: manifest.Config.BoardIssueID,
		Phase:        phaseState.Phase,
		Workers:      manifest.Workers,
		Tasks:        taskList,
		FixLoopCount: phaseState.FixLoopCount,
	}

	// Marshal and print as JSON
	statusJSON, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling status: %w", err)
	}

	fmt.Println(string(statusJSON))
	return nil
}

// cancelTeam cancels a running team.
func cancelTeam(cmd *cobra.Command, args []string) error {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("getting config flag: %w", err)
	}

	teamName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("getting name flag: %w", err)
	}

	// Parse config
	cfg, err := config.ParseWorkflow(cfgPath)
	if err != nil {
		return fmt.Errorf("parsing workflow config: %w", err)
	}

	// Create paths and store
	paths := team.NewPaths(cfg.TeamStateDir())
	store := team.NewStore(paths)

	// Create phase machine
	tasks := team.NewTaskRegistry(store, paths, cfg.TeamClaimLeaseSeconds())
	phases := team.NewPhaseMachine(store, tasks, cfg.TeamMaxFixLoops())

	// Cancel the team
	if err := phases.Cancel(teamName, "cancelled by user"); err != nil {
		return fmt.Errorf("cancelling team: %w", err)
	}

	fmt.Printf("Team %q cancelled successfully\n", teamName)
	return nil
}
