package team

import (
	"errors"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

var (
	ErrInvalidTransition = errors.New("invalid phase transition")
	ErrFixLoopExceeded   = errors.New("fix loop count exceeded maximum")
	ErrPhaseTerminal     = errors.New("phase is terminal, no transitions allowed")
)

// PhaseMachine manages team phase transitions with validation and fix-loop bounding.
type PhaseMachine struct {
	store       *Store
	tasks       *TaskRegistry
	maxFixLoops int
}

// NewPhaseMachine creates a PhaseMachine with the given max fix-loop bound.
func NewPhaseMachine(store *Store, tasks *TaskRegistry, maxFixLoops int) *PhaseMachine {
	return &PhaseMachine{
		store:       store,
		tasks:       tasks,
		maxFixLoops: maxFixLoops,
	}
}

// CurrentPhase reads the current phase from disk.
func (m *PhaseMachine) CurrentPhase(teamName string) (types.TeamPhase, error) {
	state, err := m.store.LoadPhaseState(teamName)
	if err != nil {
		return "", fmt.Errorf("load phase state: %w", err)
	}
	return state.Phase, nil
}

// Transition attempts to move to a new phase.
// It validates:
// 1. Current phase is not terminal
// 2. Target phase is in the valid transitions list
// 3. Fix loop hasn't exceeded max (when transitioning to PhaseFix)
// 4. Records the transition in history
func (m *PhaseMachine) Transition(teamName string, to types.TeamPhase, reason string) error {
	return m.store.UpdatePhaseState(teamName, func(state *types.TeamPhaseState) error {
		from := state.Phase

		if from.IsTerminal() {
			return ErrPhaseTerminal
		}

		valid := false
		for _, allowed := range from.ValidTransitions() {
			if allowed == to {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
		}

		next := to
		nextReason := reason
		if next == types.PhaseFix {
			if state.FixLoopCount >= m.maxFixLoops {
				next = types.PhaseFailed
				nextReason = fmt.Sprintf("%v (%d): %s", ErrFixLoopExceeded, m.maxFixLoops, reason)
			} else {
				state.FixLoopCount++
			}
		}

		state.Transitions = append(state.Transitions, types.PhaseTransition{
			From:      from,
			To:        next,
			Reason:    nextReason,
			Timestamp: time.Now(),
		})
		state.Phase = next
		return nil
	})
}

// SetArtifact stores a named artifact (e.g., plan document path) in the phase state.
func (m *PhaseMachine) SetArtifact(teamName, key, value string) error {
	return m.store.UpdatePhaseState(teamName, func(state *types.TeamPhaseState) error {
		if state.Artifacts == nil {
			state.Artifacts = map[string]string{}
		}
		state.Artifacts[key] = value
		return nil
	})
}

// GetArtifact retrieves a named artifact from the phase state.
func (m *PhaseMachine) GetArtifact(teamName, key string) (string, error) {
	state, err := m.store.LoadPhaseState(teamName)
	if err != nil {
		return "", fmt.Errorf("load phase state: %w", err)
	}
	return state.Artifacts[key], nil
}

// AllTasksTerminal returns true if every task is completed or failed.
func (m *PhaseMachine) AllTasksTerminal(teamName string) (bool, error) {
	tasks, err := m.tasks.ListTasks(teamName)
	if err != nil {
		return false, err
	}
	if len(tasks) == 0 {
		return true, nil
	}
	for _, task := range tasks {
		if task.Status != types.TaskCompleted && task.Status != types.TaskFailed {
			return false, nil
		}
	}
	return true, nil
}

// AllTasksCompleted returns true if every task is completed (none failed).
func (m *PhaseMachine) AllTasksCompleted(teamName string) (bool, error) {
	tasks, err := m.tasks.ListTasks(teamName)
	if err != nil {
		return false, err
	}
	if len(tasks) == 0 {
		return true, nil
	}
	for _, task := range tasks {
		if task.Status != types.TaskCompleted {
			return false, nil
		}
	}
	return true, nil
}

// AnyTaskFailed returns true if any task has failed.
func (m *PhaseMachine) AnyTaskFailed(teamName string) (bool, error) {
	tasks, err := m.tasks.ListTasks(teamName)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.Status == types.TaskFailed {
			return true, nil
		}
	}
	return false, nil
}

// FixLoopCount returns the current fix loop iteration count.
func (m *PhaseMachine) FixLoopCount(teamName string) (int, error) {
	state, err := m.store.LoadPhaseState(teamName)
	if err != nil {
		return 0, err
	}
	return state.FixLoopCount, nil
}

// Cancel transitions the team to the cancelled state from any non-terminal phase.
func (m *PhaseMachine) Cancel(teamName, reason string) error {
	return m.Transition(teamName, types.PhaseCancelled, reason)
}

// TransitionHistory returns the full list of phase transitions.
func (m *PhaseMachine) TransitionHistory(teamName string) ([]types.PhaseTransition, error) {
	state, err := m.store.LoadPhaseState(teamName)
	if err != nil {
		return nil, err
	}
	return state.Transitions, nil
}
