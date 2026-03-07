package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/tui"
	"github.com/junhoyeo/contrabass/internal/types"
)

const teamEventBufferSize = 256

var (
	startTUITeamEventBridge = tui.StartTeamEventBridge
	dispatchRootBoardIssues = dispatchBoardIssues
	runRootTeamIssue        = runTeamWithHooks
)

func runTeamExecutionApp(
	ctx context.Context,
	cfgPath string,
	watcher *config.Watcher,
	logger *log.Logger,
	noTUI bool,
	dryRun bool,
) error {
	if watcher == nil {
		return errors.New("config watcher is required for team execution")
	}

	if dryRun {
		return runTeamExecutionLoop(ctx, cfgPath, watcher, nil, true)
	}

	if noTUI {
		return runTeamExecutionLoop(ctx, cfgPath, watcher, nil, false)
	}

	teamEvents := make(chan types.TeamEvent, teamEventBufferSize)
	cfg := watcher.GetConfig()
	return runTeamTUI(ctx, cfg, teamEvents, func(runCtx context.Context) error {
		defer close(teamEvents)
		return runTeamExecutionLoop(runCtx, cfgPath, watcher, teamEvents, false)
	})
}

func runTeamExecutionLoop(
	ctx context.Context,
	cfgPath string,
	watcher *config.Watcher,
	teamEvents chan<- types.TeamEvent,
	singlePoll bool,
) error {
	for {
		cfg := watcher.GetConfig()
		if cfg == nil {
			return errors.New("workflow config is unavailable")
		}
		if err := validateTeamExecutionConfig(cfg); err != nil {
			return err
		}

		hooks := teamRunHooks{
			ParentContext:        ctx,
			DisableSignalHandler: true,
		}
		if teamEvents != nil {
			hooks.EventHandlers = append(hooks.EventHandlers, func(_ context.Context, event types.TeamEvent) {
				select {
				case <-ctx.Done():
				case teamEvents <- event:
				}
			})
		}

		if err := dispatchRootBoardIssues(
			ctx,
			io.Discard,
			newLocalBoardTracker(cfg),
			boardDispatchOptions{
				ConfigPath: cfgPath,
				UntilEmpty: true,
			},
			func(opts teamRunOptions) error {
				return runRootTeamIssue(opts, hooks)
			},
		); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		if singlePoll {
			return nil
		}

		pollInterval := time.Duration(cfg.PollIntervalMs()) * time.Millisecond
		if pollInterval <= 0 {
			pollInterval = time.Second
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval):
		}
	}
}

func runTeamTUI(
	ctx context.Context,
	cfg *config.WorkflowConfig,
	teamEvents <-chan types.TeamEvent,
	runner func(context.Context) error,
) error {
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	model := tui.NewModel()
	p := tea.NewProgram(withViewportProgramOptions(model))

	statusEvents := make(chan orchestrator.OrchestratorEvent, 1)
	statusEvents <- teamExecutionStatusEvent(cfg)
	close(statusEvents)

	startTUIEventBridge(tuiCtx, p, statusEvents)
	startTUITeamEventBridge(tuiCtx, p, teamEvents)

	runDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				runDone <- fmt.Errorf("team runtime panic: %v", r)
			}
		}()
		runDone <- runner(tuiCtx)
	}()

	_, tuiErr := runTUIProgram(p)
	tui.CleanupNativeImage()

	tuiCancel()
	select {
	case runErr := <-runDone:
		if runErr != nil {
			if tuiErr != nil {
				return fmt.Errorf("team runtime failed: %w (tui error: %v)", runErr, tuiErr)
			}
			return runErr
		}
	case <-time.After(runTUIShutdownTimeout):
		if tuiErr != nil {
			return fmt.Errorf("timed out waiting for team runtime shutdown: %w", tuiErr)
		}
		return errors.New("timed out waiting for team runtime shutdown")
	}

	return tuiErr
}

func validateTeamExecutionConfig(cfg *config.WorkflowConfig) error {
	if cfg == nil {
		return errors.New("workflow config is nil")
	}

	switch cfg.TrackerType() {
	case "internal", "local":
		return nil
	default:
		return fmt.Errorf(
			"team execution requires tracker.type internal/local, got %q",
			cfg.TrackerType(),
		)
	}
}

func newLocalBoardTracker(cfg *config.WorkflowConfig) *tracker.LocalTracker {
	actor := os.Getenv("TRACKER_ACTOR")
	if actor == "" && cfg != nil {
		actor = cfg.GitHubAssignee()
	}

	return tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    cfg.LocalBoardDir(),
		IssuePrefix: cfg.LocalIssuePrefix(),
		Actor:       actor,
	})
}

func teamExecutionStatusEvent(cfg *config.WorkflowConfig) orchestrator.OrchestratorEvent {
	if cfg == nil {
		cfg = &config.WorkflowConfig{}
	}

	modelName, _ := cfg.Model()
	projectURL := cfg.TrackerProjectURL()
	trackerType := cfg.TrackerType()
	trackerScope := projectURL
	if trackerType == "internal" || trackerType == "local" {
		trackerScope = cfg.LocalBoardDir()
	}

	return orchestrator.OrchestratorEvent{
		Type: orchestrator.EventStatusUpdate,
		Data: orchestrator.StatusUpdate{
			Stats: orchestrator.Stats{
				MaxAgents: cfg.TeamMaxWorkers(),
				StartTime: time.Now(),
			},
			ModelName:    modelName,
			ProjectURL:   projectURL,
			TrackerType:  trackerType,
			TrackerScope: trackerScope,
		},
	}
}
