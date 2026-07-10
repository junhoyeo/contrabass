export interface Stats {
  Running: number;
  MaxAgents: number;
  TotalTokensIn: number;
  TotalTokensOut: number;
  StartTime: string;
  PollCount: number;
}
export interface RunningEntry {
  issue_id: string;
  attempt: number;
  pid: number;
  session_id: string;
  workspace: string;
  started_at: string;
  phase: number;
  tokens_in: number;
  tokens_out: number;
  // Liveness + diff (already on wire from internal/orchestrator/snapshot.go)
  phase_label?: string;
  last_activity_at?: string;
  last_activity_kind?: string;
  diff_added?: number;
  diff_removed?: number;
  diff_files?: number;
  diff_status?: string;
  last_heartbeat_at?: string;
  iteration?: number;
  iteration_max?: number;
  // Task phase + ETA (surface-task-phase-and-eta)
  agent_stage?: string;
  agent_stage_step?: number;
  eta_completion_at?: string;
  eta_confidence?: string;
}

export type DetailSelectionKind =
  | "running"
  | "backoff"
  | "todo"
  | "done"
  | "canceled";

export interface DetailSelection {
  kind: DetailSelectionKind;
  issueId: string;
}

export interface SheetData {
  kind: DetailSelectionKind;
  issue?: Issue;
  running?: RunningEntry;
  backoff?: BackoffEntry;
}
export interface BackoffEntry {
  issue_id: string;
  attempt: number;
  retry_at: string;
  error: string;
}
export interface Issue {
  id: string;
  identifier?: string;
  title: string;
  description: string;
  state: number;
  priority?: number;
  labels: string[];
  url: string;
  branch_name?: string;
  blocked_by?: string[];
  created_at?: string;
  updated_at?: string;
  tracker_meta: Record<string, unknown>;
}

export interface LinearUserSummary {
  id: string;
  name?: string;
  display_name?: string;
}
export interface LinearNamedRef {
  id: string;
  key?: string;
  name?: string;
  url?: string;
}
export interface LinearCycleSummary {
  id: string;
  name?: string;
  number?: number;
  starts_at?: string;
  ends_at?: string;
}
export interface LinearRelatedIssueSummary {
  id: string;
  identifier?: string;
  title?: string;
  url?: string;
  state?: string;
  state_type?: string;
}
export interface LinearRelationSummary {
  type: string;
  direction: string;
  issue: LinearRelatedIssueSummary;
}
export interface LinearIssueDetail {
  assignee?: LinearUserSummary;
  creator?: LinearUserSummary;
  team?: LinearNamedRef;
  project?: LinearNamedRef;
  cycle?: LinearCycleSummary;
  estimate?: number;
  due_date?: string;
  relations: LinearRelationSummary[];
  fetched_at: string;
}
export interface IssueDetailResponse {
  issue: Issue;
  linear?: LinearIssueDetail;
  generated_at: string;
  error?: string;
}

export interface WorkflowTimelineSnapshot {
  issue_id: string;
  runs: WorkflowRunSummary[];
  nodes: WorkflowNodeSummary[];
  run_sync_states: RunSyncState[];
  node_sync_states: NodeSyncState[];
  generated_at: string;
}
export interface WorkflowRunSummary {
  issue_id?: string;
  run_id: string;
  attempt?: number;
  status?: string;
  started_at?: string;
  completed_at?: string;
  summary?: string;
}
export interface WorkflowNodeSummary {
  issue_id?: string;
  run_id: string;
  node_id: string;
  attempt?: number;
  status: string;
  title?: string;
  summary?: string;
  body?: string;
  content_hash?: string;
  started_at?: string;
  completed_at?: string;
  syncable?: boolean;
}
export interface RunSyncState {
  issue_id?: string;
  run_id: string;
  target: string;
  status: string;
  comment_id?: string;
  comment_url?: string;
  retry_after?: string;
  error?: string;
  last_error?: string;
  updated_at?: string;
}
export interface NodeSyncState {
  issue_id?: string;
  run_id: string;
  node_id: string;
  attempt?: number;
  target: string;
  status: string;
  comment_id?: string;
  comment_url?: string;
  retry_after?: string;
  error?: string;
  last_error?: string;
  updated_at?: string;
}

export interface OrchestratorEvent {
  Type: number;
  IssueID: string;
  Data: unknown;
  Timestamp: string;
}
export interface BuildInfo {
  version: string;
  commit: string;
  date: string;
}

export interface StateSnapshot {
  stats: Stats;
  running: RunningEntry[];
  backoff: BackoffEntry[];
  issues: Record<string, Issue>;
  generated_at: string;
  build_info?: BuildInfo;
}

export interface TeamPhaseState {
  phase: string;
  fix_loop_count: number;
  transitions: PhaseTransition[];
  artifacts: Record<string, string>;
}
export interface PhaseTransition {
  from: string;
  to: string;
  reason: string;
  timestamp: string;
}
export interface TeamTask {
  id: string;
  subject: string;
  description: string;
  status: string;
  blocked_by?: string[];
  depends_on?: string[];
  claim?: TaskClaim;
  version: number;
  created_at: string;
  updated_at: string;
  result?: string;
  file_ownership?: string[];
}
export interface TaskClaim {
  worker_id: string;
  token: string;
  leased_at: string;
}
export interface WorkerState {
  id: string;
  agent_type: string;
  status: string;
  current_task?: string;
  work_dir: string;
  pid?: number;
  started_at: string;
  last_heartbeat: string;
}
export interface TeamSnapshot {
  name: string;
  phase: TeamPhaseState;
  workers: WorkerState[];
  tasks: TeamTask[];
  config: TeamConfig;
  created_at: string;
}
export interface TeamConfig {
  max_workers: number;
  max_fix_loops: number;
  claim_lease_seconds: number;
  state_dir: string;
  agent_type: string;
  board_issue_id?: string;
}

export interface BoardIssue {
  id: string;
  identifier: string;
  title: string;
  description: string;
  state: string;
  parent_id?: string;
  child_ids?: string[];
  assignee?: string;
  labels?: string[];
  url?: string;
  branch_name?: string;
  blocked_by?: string[];
  claimed_by?: string;
  created_at: string;
  updated_at: string;
}
export interface BoardEvent {
  action: string;
  issue: BoardIssue;
}

export interface AgentLogEvent {
  worker_id: string;
  line: string;
  stream: string;
  timestamp: string;
}

export type WebEventKind = "orchestrator" | "team" | "board" | "agent_log";
export interface WebEvent {
  kind: WebEventKind;
  type: string;
  payload: unknown;
  timestamp: string;
}

export interface MCPAgentServerConfig {
  type: string;
  url: string;
  headers?: Record<string, string>;
}

export interface MCPAgentConfig {
  mcpServers: Record<string, MCPAgentServerConfig>;
}

export interface MCPConfigResponse {
  server_name: string;
  transport: string;
  url: string;
  protocol_version: string;
  token_required: boolean;
  token?: string;
  authorization_header?: string;
  expires_at?: string;
  generated_at: string;
  expires_in_seconds?: number;
  regenerate_endpoint: string;
  config: MCPAgentConfig;
}
