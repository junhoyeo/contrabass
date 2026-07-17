package agent_test

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/team"
	"github.com/junhoyeo/contrabass/internal/tmux"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type integrationMockCall struct {
	name string
	args []string
}

type integrationMockRunner struct {
	mu       sync.Mutex
	calls    []integrationMockCall
	handlers map[string]func(args []string) ([]byte, error)
}

func (m *integrationMockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	callArgs := append([]string(nil), args...)
	m.calls = append(m.calls, integrationMockCall{name: name, args: callArgs})

	key := name + " " + strings.Join(args, " ")
	handler, ok := m.handlers[key]
	if !ok {
		return nil, nil
	}

	return handler(callArgs)
}

func (m *integrationMockRunner) Calls() []integrationMockCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	calls := make([]integrationMockCall, 0, len(m.calls))
	for _, call := range m.calls {
		calls = append(calls, integrationMockCall{name: call.name, args: append([]string(nil), call.args...)})
	}

	return calls
}

func TestTmuxRunner_IntegrationPipeline(t *testing.T) {
	tests := []struct {
		name                string
		processCount        int
		cleanupWithClose    bool
		expectedKillPaneCnt int
	}{
		{
			name:                "stop one process after integration events",
			processCount:        1,
			cleanupWithClose:    false,
			expectedKillPaneCnt: 1,
		},
		{
			name:                "close cleans up all active processes",
			processCount:        2,
			cleanupWithClose:    true,
			expectedKillPaneCnt: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			stateDir := t.TempDir()
			mockRunner := newIntegrationMockRunner(tt.processCount)

			teamName := "team-integration"
			paths := team.NewPaths(stateDir)
			store := team.NewStore(paths)
			eventLogger := team.NewEventLogger(paths)
			heartbeatMonitor := team.NewHeartbeatMonitor(store, paths, time.Minute)
			dispatchQueue := team.NewDispatchQueue(store, paths, time.Minute)
			eventConsumer := team.NewEventConsumer(eventLogger, teamName, 5*time.Millisecond)

			consumerCtx, cancelConsumer := context.WithCancel(context.Background())
			consumerDone := make(chan error, 1)
			go func() {
				consumerDone <- eventConsumer.Run(consumerCtx)
			}()
			defer func() {
				cancelConsumer()
				require.NoError(t, <-consumerDone)
			}()

			sub := eventConsumer.Subscribe()

			runner := agent.NewTmuxRunner(agent.TmuxRunnerConfig{
				TeamName:         teamName,
				AgentType:        "codex",
				Session:          tmux.NewSession(teamName, mockRunner),
				Registry:         tmux.NewCLIRegistry(),
				HeartbeatMonitor: heartbeatMonitor,
				EventLogger:      eventLogger,
				DispatchQueue:    dispatchQueue,
				PollInterval:     10 * time.Millisecond,
				Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
			})

			procs := make([]*agent.AgentProcess, 0, tt.processCount)
			for i := range tt.processCount {
				proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-INT-" + strconv.Itoa(i+1)}, workspace, "execute integration task")
				require.NoError(t, err)
				procs = append(procs, proc)
			}

			assert.Eventually(t, func() bool {
				events, err := eventLogger.Read(teamName, &team.EventFilter{Type: "worker_started"})
				if err != nil {
					return false
				}
				return len(events) == tt.processCount
			}, time.Second, 10*time.Millisecond)

			assert.Eventually(t, func() bool {
				heartbeats, err := heartbeatMonitor.ListAll(teamName)
				if err != nil {
					return false
				}
				if len(heartbeats) != tt.processCount {
					return false
				}
				for _, hb := range heartbeats {
					if hb.Status != "running" {
						return false
					}
					// Runner heartbeats must stay out of the coordinator's
					// "worker-<n>" namespace so they cannot mask a genuinely
					// stale coordinator worker.
					if !strings.HasPrefix(hb.WorkerID, "tmux-worker-") {
						return false
					}
				}
				return true
			}, time.Second, 10*time.Millisecond)

			assert.Eventually(t, func() bool {
				entries, err := dispatchQueue.ListAll(teamName)
				if err != nil {
					return false
				}
				if len(entries) != tt.processCount {
					return false
				}
				for _, entry := range entries {
					if entry.Status != team.DispatchStatusAcked {
						return false
					}
					if entry.AckedAt == nil {
						return false
					}
				}
				return true
			}, time.Second, 10*time.Millisecond)

			receivedStarted := 0
			deadline := time.After(time.Second)
			for receivedStarted < tt.processCount {
				select {
				case event := <-sub:
					if event.Type == "worker_started" {
						receivedStarted++
					}
				case <-deadline:
					t.Fatalf("timed out waiting for worker_started events")
				}
			}

			if tt.cleanupWithClose {
				require.NoError(t, runner.Close())
			} else {
				require.NoError(t, runner.Stop(procs[0]))
			}

			for _, proc := range procs {
				select {
				case err := <-proc.Done:
					require.NoError(t, err)
				case <-time.After(time.Second):
					t.Fatalf("timed out waiting for process completion")
				}
			}

			killPaneCalls := 0
			for _, call := range mockRunner.Calls() {
				if call.name == "tmux" && len(call.args) >= 3 && call.args[0] == "kill-pane" {
					killPaneCalls++
				}
			}
			assert.Equal(t, tt.expectedKillPaneCnt, killPaneCalls)
		})
	}
}

func newIntegrationMockRunner(processCount int) *integrationMockRunner {
	handlers := map[string]func(args []string) ([]byte, error){
		"tmux list-panes -t %1 -F #{pane_dead}": func(_ []string) ([]byte, error) {
			return []byte("0\n"), nil
		},
		"tmux kill-pane -t %1": func(_ []string) ([]byte, error) {
			return nil, nil
		},
	}

	handlers["tmux new-window -t =contrabass-team-integration -n tmux-worker-1 -P -F #{pane_id}"] = func(_ []string) ([]byte, error) {
		return []byte("%1\n"), nil
	}

	if processCount > 1 {
		handlers["tmux new-window -t =contrabass-team-integration -n tmux-worker-2 -P -F #{pane_id}"] = func(_ []string) ([]byte, error) {
			return []byte("%2\n"), nil
		}
		handlers["tmux list-panes -t %2 -F #{pane_dead}"] = func(_ []string) ([]byte, error) {
			return []byte("0\n"), nil
		}
		handlers["tmux kill-pane -t %2"] = func(_ []string) ([]byte, error) {
			return nil, nil
		}
	}

	return &integrationMockRunner{handlers: handlers}
}
