package types

import (
	"time"
)

type TeamPhase string

const (
	PhasePlan      TeamPhase = "team-plan"
	PhasePRD       TeamPhase = "team-prd"
	PhaseExec      TeamPhase = "team-exec"
	PhaseVerify    TeamPhase = "team-verify"
	PhaseFix       TeamPhase = "team-fix"
	PhaseComplete  TeamPhase = "complete"
	PhaseFailed    TeamPhase = "failed"
	PhaseCancelled TeamPhase = "cancelled"
)

func (p TeamPhase) IsTerminal() bool {
	switch p {
	case PhaseComplete, PhaseFailed, PhaseCancelled:
		return true
	default:
		return false
	}
}

func (p TeamPhase) String() string {
	return string(p)
}

// ValidTransitions returns the valid next phases from the current phase.
func (p TeamPhase) ValidTransitions() []TeamPhase {
	switch p {
	case PhasePlan:
		return []TeamPhase{PhasePRD, PhaseCancelled}
	case PhasePRD:
		return []TeamPhase{PhaseExec, PhaseCancelled}
	case PhaseExec:
		return []TeamPhase{PhaseVerify, PhaseCancelled}
	case PhaseVerify:
		return []TeamPhase{PhaseFix, PhaseComplete, PhaseFailed, PhaseCancelled}
	case PhaseFix:
		return []TeamPhase{PhaseExec, PhaseVerify, PhaseComplete, PhaseFailed, PhaseCancelled}
	default:
		return nil
	}
}

type TeamTaskStatus string

const (
	TaskPending    TeamTaskStatus = "pending"
	TaskInProgress TeamTaskStatus = "in_progress"
	TaskCompleted  TeamTaskStatus = "completed"
	TaskFailed     TeamTaskStatus = "failed"
)

type MailboxMessageStatus string

const (
	MessagePending      MailboxMessageStatus = "pending"
	MessageDelivered    MailboxMessageStatus = "delivered"
	MessageAcknowledged MailboxMessageStatus = "acknowledged"
)

type TaskClaim struct {
	WorkerID string    `json:"worker_id"`
	Token    string    `json:"token"`
	LeasedAt time.Time `json:"leased_at"`
}

type TeamTask struct {
	ID            string         `json:"id"`
	Subject       string         `json:"subject"`
	Description   string         `json:"description"`
	Status        TeamTaskStatus `json:"status"`
	BlockedBy     []string       `json:"blocked_by,omitempty"`
	DependsOn     []string       `json:"depends_on,omitempty"`
	Claim         *TaskClaim     `json:"claim,omitempty"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Result        string         `json:"result,omitempty"`
	FileOwnership []string       `json:"file_ownership,omitempty"`
}

type WorkerState struct {
	ID            string    `json:"id"`
	AgentType     string    `json:"agent_type"`
	Status        string    `json:"status"`
	CurrentTask   string    `json:"current_task,omitempty"`
	WorkDir       string    `json:"work_dir"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

type MailboxMessage struct {
	ID        string               `json:"id"`
	From      string               `json:"from"`
	To        string               `json:"to"`
	Content   string               `json:"content"`
	Timestamp time.Time            `json:"timestamp"`
	Status    MailboxMessageStatus `json:"status"`
}

type PhaseTransition struct {
	From      TeamPhase `json:"from"`
	To        TeamPhase `json:"to"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

type TeamPhaseState struct {
	Phase        TeamPhase         `json:"phase"`
	Artifacts    map[string]string `json:"artifacts,omitempty"`
	Transitions  []PhaseTransition `json:"transitions"`
	FixLoopCount int               `json:"fix_loop_count"`
}

type TeamManifest struct {
	Name      string        `json:"name"`
	Workers   []WorkerState `json:"workers"`
	Config    TeamConfig    `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type TeamConfig struct {
	MaxWorkers        int    `json:"max_workers"`
	MaxFixLoops       int    `json:"max_fix_loops"`
	ClaimLeaseSeconds int    `json:"claim_lease_seconds"`
	StateDir          string `json:"state_dir"`
	AgentType         string `json:"agent_type"`
	BoardIssueID      string `json:"board_issue_id,omitempty"`
}

type FileOwnership struct {
	Path      string    `json:"path"`
	WorkerID  string    `json:"worker_id"`
	TaskID    string    `json:"task_id"`
	ClaimedAt time.Time `json:"claimed_at"`
}

type TeamEvent struct {
	Type      string                 `json:"type"`
	TeamName  string                 `json:"team_name"`
	Data      map[string]interface{} `json:"data"`
	Timestamp time.Time              `json:"timestamp"`
}
