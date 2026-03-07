export interface Stats { Running: number; MaxAgents: number; TotalTokensIn: number; TotalTokensOut: number; StartTime: string; PollCount: number; }
export interface RunningEntry { issue_id: string; attempt: number; pid: number; session_id: string; workspace: string; started_at: string; phase: number; tokens_in: number; tokens_out: number; }
export interface BackoffEntry { issue_id: string; attempt: number; retry_at: string; error: string; }
export interface Issue { id: string; title: string; description: string; state: number; labels: string[]; url: string; tracker_meta: Record<string, unknown>; }
export interface OrchestratorEvent { Type: number; IssueID: string; Data: unknown; Timestamp: string; }
export interface StateSnapshot { stats: Stats; running: RunningEntry[]; backoff: BackoffEntry[]; issues: Record<string, Issue>; generated_at: string; }

export interface TeamPhaseState { phase: string; fix_loop_count: number; transitions: PhaseTransition[]; artifacts: Record<string, string>; }
export interface PhaseTransition { from: string; to: string; reason: string; timestamp: string; }
export interface TeamTask { id: string; subject: string; description: string; status: string; blocked_by?: string[]; depends_on?: string[]; claim?: TaskClaim; version: number; created_at: string; updated_at: string; result?: string; file_ownership?: string[]; }
export interface TaskClaim { worker_id: string; token: string; leased_at: string; }
export interface WorkerState { id: string; agent_type: string; status: string; current_task?: string; work_dir: string; pid?: number; started_at: string; last_heartbeat: string; }
export interface TeamSnapshot { name: string; phase: TeamPhaseState; workers: WorkerState[]; tasks: TeamTask[]; config: TeamConfig; created_at: string; }
export interface TeamConfig { max_workers: number; max_fix_loops: number; claim_lease_seconds: number; state_dir: string; agent_type: string; board_issue_id?: string; }

export interface BoardIssue { id: string; identifier: string; title: string; description: string; state: string; parent_id?: string; child_ids?: string[]; assignee?: string; labels?: string[]; url?: string; branch_name?: string; blocked_by?: string[]; claimed_by?: string; created_at: string; updated_at: string; }
export interface BoardEvent { action: string; issue: BoardIssue; }

export interface AgentLogEvent { worker_id: string; line: string; stream: string; timestamp: string; }

export type WebEventKind = 'orchestrator' | 'team' | 'board' | 'agent_log';
export interface WebEvent { kind: WebEventKind; type: string; payload: unknown; timestamp: string; }
