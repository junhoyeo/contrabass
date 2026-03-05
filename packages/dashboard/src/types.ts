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
}

export interface BackoffEntry {
  issue_id: string;
  attempt: number;
  retry_at: string;
  error: string;
}

export interface Issue {
  id: string;
  title: string;
  description: string;
  state: number;
  labels: string[];
  url: string;
  tracker_meta: Record<string, unknown>;
}

export interface OrchestratorEvent {
  Type: number;
  IssueID: string;
  Data: unknown;
  Timestamp: string;
}

export interface StateSnapshot {
  stats: Stats;
  running: RunningEntry[];
  backoff: BackoffEntry[];
  issues: Record<string, Issue>;
  generated_at: string;
}
