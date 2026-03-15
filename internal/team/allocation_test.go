package team

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreWorker(t *testing.T) {
	tests := []struct {
		name              string
		task              AllocationTaskInput
		workerRole        string
		workerPrimaryRole string
		assignedCount     int
		expectedScore     int
	}{
		{
			name:       "primary and worker role match",
			task:       AllocationTaskInput{Role: "executor"},
			workerRole: "executor", workerPrimaryRole: "executor",
			assignedCount: 0,
			expectedScore: 30,
		},
		{
			name:       "no match with assignment penalty",
			task:       AllocationTaskInput{Role: "security-reviewer"},
			workerRole: "executor", workerPrimaryRole: "executor",
			assignedCount: 2,
			expectedScore: -8,
		},
		{
			name:       "blocked task adds additional penalty",
			task:       AllocationTaskInput{Role: "executor", BlockedBy: []string{"task-0"}},
			workerRole: "executor", workerPrimaryRole: "executor",
			assignedCount: 3,
			expectedScore: 15,
		},
		{
			name:       "unassigned worker bonus when no primary role",
			task:       AllocationTaskInput{Role: "executor"},
			workerRole: "worker", workerPrimaryRole: "",
			assignedCount: 0,
			expectedScore: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreWorker(tt.task, "worker-1", tt.workerRole, tt.workerPrimaryRole, tt.assignedCount)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func TestChooseTaskOwner(t *testing.T) {
	tests := []struct {
		name               string
		task               AllocationTaskInput
		workers            []AllocationWorkerInput
		currentAssignments []AllocationDecision
		expectedOwner      string
	}{
		{
			name:          "single worker",
			task:          AllocationTaskInput{ID: "task-1"},
			workers:       []AllocationWorkerInput{{Name: "worker-1", Role: "executor", PrimaryRole: "executor"}},
			expectedOwner: "worker-1",
		},
		{
			name: "multiple workers picks role match",
			task: AllocationTaskInput{ID: "task-1", Role: "security-reviewer"},
			workers: []AllocationWorkerInput{
				{Name: "worker-a", Role: "executor", PrimaryRole: "executor"},
				{Name: "worker-b", Role: "security-reviewer", PrimaryRole: "security-reviewer"},
			},
			expectedOwner: "worker-b",
		},
		{
			name: "load balancing favors lower load",
			task: AllocationTaskInput{ID: "task-1"},
			workers: []AllocationWorkerInput{
				{Name: "worker-a"},
				{Name: "worker-b"},
			},
			currentAssignments: []AllocationDecision{{Owner: "worker-a"}, {Owner: "worker-a"}},
			expectedOwner:      "worker-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ChooseTaskOwner(tt.task, tt.workers, tt.currentAssignments)
			assert.Equal(t, tt.expectedOwner, decision.Owner)
			assert.Contains(t, decision.Reason, "selected")
		})
	}
}

func TestAllocateTasksToWorkers(t *testing.T) {
	tasks := []AllocationTaskInput{
		{ID: "task-1"},
		{ID: "task-2"},
		{ID: "task-3"},
		{ID: "task-4"},
	}
	workers := []AllocationWorkerInput{
		{Name: "worker-a"},
		{Name: "worker-b"},
	}

	decisions := AllocateTasksToWorkers(tasks, workers)
	require.Len(t, decisions, 4)

	counts := map[string]int{}
	for _, decision := range decisions {
		counts[decision.Owner]++
	}

	assert.Equal(t, 2, counts["worker-a"])
	assert.Equal(t, 2, counts["worker-b"])
}

func TestHasCompletedDependencies(t *testing.T) {
	tests := []struct {
		name     string
		task     AllocationTaskInput
		taskByID map[string]AllocationTaskInput
		expected bool
	}{
		{
			name:     "no dependencies",
			task:     AllocationTaskInput{ID: "task-1"},
			taskByID: map[string]AllocationTaskInput{},
			expected: true,
		},
		{
			name: "all dependencies completed",
			task: AllocationTaskInput{ID: "task-1", DependsOn: []string{"task-a", "task-b"}},
			taskByID: map[string]AllocationTaskInput{
				"task-a": {ID: "task-a", Status: "completed"},
				"task-b": {ID: "task-b", Status: "completed"},
			},
			expected: true,
		},
		{
			name: "dependency pending",
			task: AllocationTaskInput{ID: "task-1", DependsOn: []string{"task-a"}},
			taskByID: map[string]AllocationTaskInput{
				"task-a": {ID: "task-a", Status: "pending"},
			},
			expected: false,
		},
		{
			name: "missing dependency",
			task: AllocationTaskInput{ID: "task-1", DependsOn: []string{"task-missing"}},
			taskByID: map[string]AllocationTaskInput{
				"task-a": {ID: "task-a", Status: "completed"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, HasCompletedDependencies(tt.task, tt.taskByID))
		})
	}
}

func TestBuildRebalanceDecisions(t *testing.T) {
	t.Run("basic rebalance with available workers", func(t *testing.T) {
		tasks := []AllocationTaskInput{
			{ID: "task-1", Status: "pending"},
			{ID: "task-2", Status: "pending", Owner: "worker-9"},
			{ID: "task-3", Status: "pending", DependsOn: []string{"task-4"}},
			{ID: "task-4", Status: "completed"},
		}
		workers := []RebalanceWorkerInput{
			{AllocationWorkerInput: AllocationWorkerInput{Name: "worker-a"}, Alive: true, Status: "idle"},
			{AllocationWorkerInput: AllocationWorkerInput{Name: "worker-b"}, Alive: true, Status: "working"},
		}

		decisions := BuildRebalanceDecisions(tasks, workers, nil)
		require.Len(t, decisions, 2)
		assert.Equal(t, "task-1", decisions[0].TaskID)
		assert.Equal(t, "task-3", decisions[1].TaskID)
		assert.Equal(t, "recommend", decisions[0].Type)
		assert.Equal(t, "recommend", decisions[1].Type)
		assert.Equal(t, "worker-a", decisions[0].WorkerName)
	})

	t.Run("reclaimed tasks prioritized and marked assign", func(t *testing.T) {
		tasks := []AllocationTaskInput{
			{ID: "task-b", Status: "pending"},
			{ID: "task-a", Status: "pending"},
		}
		workers := []RebalanceWorkerInput{
			{AllocationWorkerInput: AllocationWorkerInput{Name: "worker-a"}, Alive: true, Status: "unknown"},
		}

		decisions := BuildRebalanceDecisions(tasks, workers, []string{"task-b"})
		require.Len(t, decisions, 2)
		assert.Equal(t, "task-b", decisions[0].TaskID)
		assert.Equal(t, "assign", decisions[0].Type)
		assert.Equal(t, "task-a", decisions[1].TaskID)
		assert.Equal(t, "recommend", decisions[1].Type)
	})

	t.Run("dead or unavailable workers skipped", func(t *testing.T) {
		tasks := []AllocationTaskInput{{ID: "task-1", Status: "pending"}}
		workers := []RebalanceWorkerInput{
			{AllocationWorkerInput: AllocationWorkerInput{Name: "worker-dead"}, Alive: false, Status: "idle"},
			{AllocationWorkerInput: AllocationWorkerInput{Name: "worker-busy"}, Alive: true, Status: "working"},
		}

		decisions := BuildRebalanceDecisions(tasks, workers, nil)
		assert.Empty(t, decisions)
	})
}
