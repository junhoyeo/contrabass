package team

import (
	"fmt"
	"sort"
	"strings"

	"github.com/junhoyeo/contrabass/internal/types"
)

type AllocationTaskInput struct {
	ID          string
	Subject     string
	Description string
	Role        string
	BlockedBy   []string
	DependsOn   []string
	Status      string
	Owner       string
}

type AllocationWorkerInput struct {
	Name        string
	Role        string
	PrimaryRole string
}

type AllocationDecision struct {
	Owner  string
	Reason string
}

type RebalanceWorkerInput struct {
	AllocationWorkerInput
	Alive  bool
	Status string
}

type RebalanceDecision struct {
	Type       string
	TaskID     string
	WorkerName string
	Reason     string
}

func scoreWorker(task AllocationTaskInput, _ string, workerRole string, workerPrimaryRole string, assignedCount int) int {
	score := 0
	taskRole := task.Role

	if taskRole != "" && workerPrimaryRole == taskRole {
		score += 18
	}
	if taskRole != "" && workerRole == taskRole {
		score += 12
	}
	if taskRole != "" && workerPrimaryRole == "" && assignedCount == 0 {
		score += 5
	}

	score -= assignedCount * 4
	if len(task.BlockedBy) > 0 {
		score -= assignedCount
	}

	return score
}

func ChooseTaskOwner(task AllocationTaskInput, workers []AllocationWorkerInput, currentAssignments []AllocationDecision) AllocationDecision {
	if len(workers) == 0 {
		return AllocationDecision{Reason: "no available workers"}
	}

	sortedWorkers := append([]AllocationWorkerInput(nil), workers...)
	sort.Slice(sortedWorkers, func(i, j int) bool {
		return sortedWorkers[i].Name < sortedWorkers[j].Name
	})

	bestOwner := ""
	bestScore := 0
	bestAssignedCount := 0

	for i, worker := range sortedWorkers {
		assignedCount := countAssignments(currentAssignments, worker.Name)
		primaryRole := worker.PrimaryRole
		workerRole := worker.Role
		if workerRole == "" {
			workerRole = primaryRole
		}
		score := scoreWorker(task, worker.Name, workerRole, primaryRole, assignedCount)

		if i == 0 || score > bestScore || (score == bestScore && (assignedCount < bestAssignedCount || (assignedCount == bestAssignedCount && worker.Name < bestOwner))) {
			bestOwner = worker.Name
			bestScore = score
			bestAssignedCount = assignedCount
		}
	}

	return AllocationDecision{
		Owner:  bestOwner,
		Reason: fmt.Sprintf("selected %s with score %d", bestOwner, bestScore),
	}
}

func AllocateTasksToWorkers(tasks []AllocationTaskInput, workers []AllocationWorkerInput) []AllocationDecision {
	decisions := make([]AllocationDecision, 0, len(tasks))
	for _, task := range tasks {
		decision := ChooseTaskOwner(task, workers, decisions)
		decisions = append(decisions, decision)
	}
	return decisions
}

func HasCompletedDependencies(task AllocationTaskInput, taskByID map[string]AllocationTaskInput) bool {
	if len(task.DependsOn) == 0 {
		return true
	}

	for _, depID := range task.DependsOn {
		depTask, ok := taskByID[depID]
		if !ok {
			return false
		}
		if !isCompletedStatus(depTask.Status) {
			return false
		}
	}

	return true
}

func BuildRebalanceDecisions(tasks []AllocationTaskInput, workers []RebalanceWorkerInput, reclaimedTaskIDs []string) []RebalanceDecision {
	availableWorkers := make([]AllocationWorkerInput, 0, len(workers))
	for _, worker := range workers {
		if !worker.Alive {
			continue
		}
		if !isAvailableWorkerStatus(worker.Status) {
			continue
		}
		availableWorkers = append(availableWorkers, worker.AllocationWorkerInput)
	}

	if len(availableWorkers) == 0 {
		return nil
	}

	taskByID := make(map[string]AllocationTaskInput, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
	}

	reclaimedSet := make(map[string]struct{}, len(reclaimedTaskIDs))
	for _, id := range reclaimedTaskIDs {
		reclaimedSet[id] = struct{}{}
	}

	candidates := make([]AllocationTaskInput, 0, len(tasks))
	for _, task := range tasks {
		if !isPendingStatus(task.Status) {
			continue
		}
		if task.Owner != "" {
			continue
		}
		if !HasCompletedDependencies(task, taskByID) {
			continue
		}
		candidates = append(candidates, task)
	}

	sort.Slice(candidates, func(i, j int) bool {
		_, iReclaimed := reclaimedSet[candidates[i].ID]
		_, jReclaimed := reclaimedSet[candidates[j].ID]
		if iReclaimed != jReclaimed {
			return iReclaimed
		}
		return candidates[i].ID < candidates[j].ID
	})

	decisions := make([]RebalanceDecision, 0, len(candidates))
	assignmentLoad := make([]AllocationDecision, 0, len(candidates))
	for _, task := range candidates {
		allocation := ChooseTaskOwner(task, availableWorkers, assignmentLoad)
		if allocation.Owner == "" {
			continue
		}

		dType := "recommend"
		if _, reclaimed := reclaimedSet[task.ID]; reclaimed {
			dType = "assign"
		}

		decisions = append(decisions, RebalanceDecision{
			Type:       dType,
			TaskID:     task.ID,
			WorkerName: allocation.Owner,
			Reason:     allocation.Reason,
		})
		assignmentLoad = append(assignmentLoad, AllocationDecision{Owner: allocation.Owner})
	}

	return decisions
}

func countAssignments(assignments []AllocationDecision, workerName string) int {
	count := 0
	for _, assignment := range assignments {
		if assignment.Owner == workerName {
			count++
		}
	}
	return count
}

func isPendingStatus(status string) bool {
	if status == "" {
		return true
	}
	return strings.EqualFold(status, string(types.TaskPending))
}

func isCompletedStatus(status string) bool {
	return strings.EqualFold(status, string(types.TaskCompleted))
}

func isAvailableWorkerStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "idle", "done", "unknown":
		return true
	default:
		return false
	}
}
