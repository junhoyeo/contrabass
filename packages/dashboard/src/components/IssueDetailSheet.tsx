import { type ReactNode, useEffect, useState } from "react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import type {
  Issue,
  IssueDetailResponse,
  LinearIssueDetail,
  NodeSyncState,
  SheetData,
  WorkflowNodeSummary,
  WorkflowTimelineSnapshot,
} from "../types";
import { getLinearState } from "./AppLayout";
import { diffSummary } from "./IssueDataTable";
import { formatElapsedSince, formatRelativeTime } from "../i18n/format";
import { zhCN } from "../i18n/messages";

interface IssueDetailSheetProps {
  data: SheetData | null;
  isStale?: boolean;
  onOpenChange: (open: boolean) => void;
}

interface AsyncResource<T> {
  loading: boolean;
  data: T | null;
  error: string | null;
}

const emptyDetailState: AsyncResource<IssueDetailResponse> = {
  loading: false,
  data: null,
  error: null,
};
const emptyTimelineState: AsyncResource<WorkflowTimelineSnapshot> = {
  loading: false,
  data: null,
  error: null,
};

function formatRetryIn(retryAt: string): string {
  const ms = Date.parse(retryAt) - Date.now();
  if (ms <= 0) return zhCN.retryQueue.ready;
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}秒后`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}分钟后`;
  const hours = Math.floor(mins / 60);
  return `${hours}小时后`;
}

async function fetchJSON<T>(url: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(url, { signal });
  const payload = (await response.json().catch(() => ({}))) as T & {
    error?: string;
  };
  if (!response.ok) {
    throw new Error(payload.error || response.statusText);
  }
  return payload as T;
}

function selectedIssueID(data: SheetData | null): string {
  return (
    data?.issue?.id ?? data?.running?.issue_id ?? data?.backoff?.issue_id ?? ""
  );
}

function displayUserName(
  user: { display_name?: string; name?: string } | undefined,
): string {
  return user?.display_name || user?.name || "—";
}

export function IssueDetailSheet({
  data,
  isStale,
  onOpenChange,
}: IssueDetailSheetProps) {
  const [debugOpen, setDebugOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | null>(null);
  const [detailState, setDetailState] =
    useState<AsyncResource<IssueDetailResponse>>(emptyDetailState);
  const [timelineState, setTimelineState] =
    useState<AsyncResource<WorkflowTimelineSnapshot>>(emptyTimelineState);

  async function handleCopyWorkspace(path: string) {
    try {
      await navigator.clipboard.writeText(path);
      setCopied(true);
      setCopyFailed(false);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopyFailed(true);
    }
  }

  const { kind, issue, running, backoff } = data ?? {};

  async function handleStopAgent(targetID: string) {
    setStopping(true);
    setStopError(null);
    try {
      const response = await fetch(
        `/api/v1/running/${encodeURIComponent(targetID)}/stop`,
        { method: "POST" },
      );
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(body.error || response.statusText);
      }
    } catch (err) {
      setStopping(false);
      setStopError(err instanceof Error ? err.message : String(err));
    }
  }

  const loadedIssue = detailState.data?.issue;
  const displayedIssue = loadedIssue ?? issue;
  const linearState = displayedIssue
    ? getLinearState(displayedIssue)
    : undefined;
  const issueID = selectedIssueID(data);

  // Stop state is per-issue: a stale error or in-flight flag from one issue
  // must not carry over when the sheet targets another one.
  useEffect(() => {
    setStopping(false);
    setStopError(null);
  }, [kind, issueID]);

  // Keyed on issueID only: `data` gets a new object identity on every SSE
  // snapshot, which would abort and restart these fetches while the sheet
  // stays on the same issue. issueID is "" whenever data is null.
  useEffect(() => {
    if (!issueID) {
      setDetailState(emptyDetailState);
      setTimelineState(emptyTimelineState);
      return;
    }

    const controller = new AbortController();
    const encodedIssueID = encodeURIComponent(issueID);

    setDetailState({ loading: true, data: null, error: null });
    setTimelineState({ loading: true, data: null, error: null });

    fetchJSON<IssueDetailResponse>(
      `/api/v1/issues/${encodedIssueID}/details`,
      controller.signal,
    )
      .then((payload) =>
        setDetailState({ loading: false, data: payload, error: null }),
      )
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setDetailState({
            loading: false,
            data: null,
            error:
              error instanceof Error ? error.message : "detail fetch failed",
          });
        }
      });

    fetchJSON<WorkflowTimelineSnapshot>(
      `/api/v1/issues/${encodedIssueID}/timeline`,
      controller.signal,
    )
      .then((payload) =>
        // Go marshals nil slices as null (issues with no workflow history);
        // normalize so render code can index the collections safely.
        setTimelineState({
          loading: false,
          data: {
            ...payload,
            runs: payload.runs ?? [],
            nodes: payload.nodes ?? [],
            run_sync_states: payload.run_sync_states ?? [],
            node_sync_states: payload.node_sync_states ?? [],
          },
          error: null,
        }),
      )
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setTimelineState({
            loading: false,
            data: null,
            error:
              error instanceof Error ? error.message : "timeline fetch failed",
          });
        }
      });

    return () => controller.abort();
  }, [issueID]);

  return (
    <Sheet open={data !== null} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 overflow-y-auto px-0 sm:max-w-[520px]"
      >
        {data ? (
          <>
            <SheetHeader className="border-b border-border/70 px-6 py-5">
              <div className="mb-1 flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs text-muted-foreground">
                  {displayedIssue?.identifier ??
                    running?.issue_id?.slice(0, 8) ??
                    backoff?.issue_id?.slice(0, 8) ??
                    "—"}
                </span>
                {isStale ? (
                  <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                    {zhCN.detail.badgeStale}
                  </span>
                ) : null}
              </div>
              <SheetTitle className="text-lg font-semibold leading-snug text-foreground">
                {displayedIssue?.title ??
                  running?.issue_id ??
                  backoff?.issue_id ??
                  "—"}
              </SheetTitle>
              <div className="mt-2 flex flex-wrap gap-2">
                {linearState ? <StatusBadge>{linearState}</StatusBadge> : null}
                {kind === "backoff" ? (
                  <StatusBadge variant="destructive">
                    {zhCN.detail.badgeBackoff}
                  </StatusBadge>
                ) : null}
                {kind === "done" ? (
                  <StatusBadge>{zhCN.detail.badgeDone}</StatusBadge>
                ) : null}
                {kind === "canceled" ? (
                  <StatusBadge variant="muted">
                    {zhCN.detail.badgeCanceled}
                  </StatusBadge>
                ) : null}
              </div>
            </SheetHeader>

            <div className="flex-1 space-y-5 px-6 py-5 text-sm">
              {kind === "running" && running ? (
                <>
                  {running.phase_label ? (
                    <DetailSection label={zhCN.detail.phaseLabel}>
                      <p className="whitespace-normal break-words text-foreground">
                        {running.phase_label}
                      </p>
                    </DetailSection>
                  ) : null}

                  {running.last_activity_at ? (
                    <DetailSection label={zhCN.detail.lastActivity}>
                      <p className="text-foreground">
                        {formatRelativeTime(running.last_activity_at)}
                        {running.last_activity_kind ? (
                          <span className="ml-2 text-xs text-muted-foreground">
                            {running.last_activity_kind}
                          </span>
                        ) : null}
                      </p>
                    </DetailSection>
                  ) : null}

                  <DetailSection label="差异">
                    <p className="font-mono text-xs text-foreground">
                      {running.diff_status && running.diff_status !== "ok"
                        ? zhCN.detail.diffUnavailable
                        : running.diff_added === 0 &&
                            running.diff_removed === 0 &&
                            running.diff_files === 0
                          ? zhCN.detail.noDiff
                          : diffSummary(running)}
                    </p>
                  </DetailSection>

                  <DetailSection label="Token">
                    <p className="font-mono text-xs text-foreground">
                      {running.tokens_in.toLocaleString()} 输入 /{" "}
                      {running.tokens_out.toLocaleString()} 输出
                    </p>
                  </DetailSection>

                  <DetailSection label="已运行">
                    <p className="font-mono text-xs text-foreground">
                      {formatElapsedSince(running.started_at)}
                    </p>
                  </DetailSection>
                </>
              ) : null}

              {kind === "backoff" && backoff ? (
                <>
                  <DetailSection label={zhCN.detail.error}>
                    <pre className="whitespace-pre-wrap break-words rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2.5 font-mono text-xs text-foreground">
                      {backoff.error || zhCN.detail.noErrorInfo}
                    </pre>
                  </DetailSection>

                  <DetailSection label="下次重试">
                    <p className="text-foreground">
                      {formatRetryIn(backoff.retry_at)}
                    </p>
                  </DetailSection>

                  <DetailSection label="重试次数">
                    <p className="font-mono text-xs text-foreground">
                      {zhCN.detail.attempt(backoff.attempt)}
                    </p>
                  </DetailSection>
                </>
              ) : null}

              {(kind === "todo" || kind === "done" || kind === "canceled") &&
              displayedIssue ? (
                <BaseIssueSections issue={displayedIssue} />
              ) : null}

              <LinearDetailSection
                issue={displayedIssue}
                linear={detailState.data?.linear}
                loading={detailState.loading}
                error={detailState.error}
              />
              <TimelineSection
                timeline={timelineState.data}
                loading={timelineState.loading}
                error={timelineState.error}
              />
            </div>

            {kind === "running" && running ? (
              <div className="border-t border-border/70 px-6 py-4">
                <button
                  type="button"
                  onClick={() => handleStopAgent(running.issue_id)}
                  disabled={stopping}
                  className="inline-flex h-9 items-center justify-center rounded-lg border border-destructive/40 bg-destructive/10 px-4 text-sm font-medium text-destructive transition hover:bg-destructive/15 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {stopping ? zhCN.detail.stopping : zhCN.detail.stopAgent}
                </button>
                {stopError ? (
                  <p className="mt-2 text-xs text-destructive">
                    {zhCN.detail.stopFailed(stopError)}
                  </p>
                ) : null}
              </div>
            ) : null}

            {kind === "running" && running ? (
              <div className="border-t border-border/70 px-6 py-4">
                <button
                  type="button"
                  aria-expanded={debugOpen}
                  aria-controls="detail-debug-panel"
                  onClick={() => setDebugOpen((v) => !v)}
                  className="flex w-full items-center justify-between text-xs font-medium text-muted-foreground hover:text-foreground"
                >
                  <span>{zhCN.detail.debugInfo}</span>
                  <span aria-hidden>{debugOpen ? "▾" : "▸"}</span>
                </button>

                <div id="detail-debug-panel" hidden={!debugOpen}>
                  <div className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-xs">
                    {running.pid > 0 ? (
                      <>
                        <span className="text-muted-foreground">PID</span>
                        <span className="font-mono text-foreground">
                          {running.pid}
                        </span>
                      </>
                    ) : null}

                    {running.session_id ? (
                      <>
                        <span className="text-muted-foreground">
                          Session ID
                        </span>
                        <span className="break-all font-mono text-foreground">
                          {running.session_id}
                        </span>
                      </>
                    ) : null}

                    {running.workspace ? (
                      <>
                        <span className="pt-0.5 text-muted-foreground">
                          {zhCN.detail.workspace}
                        </span>
                        <div className="flex min-w-0 flex-col gap-1.5">
                          <code className="break-all font-mono text-foreground">
                            {running.workspace}
                          </code>
                          <button
                            type="button"
                            onClick={() =>
                              handleCopyWorkspace(running.workspace)
                            }
                            className="w-fit rounded bg-muted/60 px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                          >
                            {copied ? zhCN.detail.copied : zhCN.detail.copyPath}
                          </button>
                          {copyFailed ? (
                            <span className="text-xs text-destructive">
                              {zhCN.detail.copyFailed}
                            </span>
                          ) : null}
                        </div>
                      </>
                    ) : null}
                  </div>
                </div>
              </div>
            ) : null}
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function BaseIssueSections({ issue }: { issue: Issue }) {
  return (
    <>
      {issue.description ? (
        <DetailSection label="描述">
          <p className="whitespace-normal break-words leading-relaxed text-muted-foreground">
            {issue.description}
          </p>
        </DetailSection>
      ) : null}

      {issue.labels?.length ? (
        <DetailSection label="标签">
          <div className="flex flex-wrap gap-1.5">
            {issue.labels.map((label) => (
              <span
                key={label}
                className="rounded-md border border-border/70 bg-muted/50 px-2 py-0.5 text-xs text-foreground"
              >
                {label}
              </span>
            ))}
          </div>
        </DetailSection>
      ) : null}

      {issue.branch_name ? (
        <DetailSection label="分支">
          <p className="break-all font-mono text-xs text-muted-foreground">
            {issue.branch_name}
          </p>
        </DetailSection>
      ) : null}

      {issue.updated_at ? (
        <DetailSection label="更新时间">
          <p className="text-xs text-muted-foreground">
            {formatRelativeTime(issue.updated_at)}
          </p>
        </DetailSection>
      ) : null}
    </>
  );
}

function LinearDetailSection({
  issue,
  linear,
  loading,
  error,
}: {
  issue?: Issue;
  linear?: LinearIssueDetail;
  loading: boolean;
  error: string | null;
}) {
  return (
    <DetailSection label={zhCN.detail.linearMetadata}>
      {loading ? (
        <p className="text-xs text-muted-foreground">
          {zhCN.detail.loadingDetails}
        </p>
      ) : null}
      {error ? (
        <InlineError message={zhCN.detail.detailLoadFailed(error)} />
      ) : null}
      {!loading && !error && !linear ? (
        <p className="text-xs text-muted-foreground">
          {zhCN.detail.noLinearDetails}
        </p>
      ) : null}
      {linear ? (
        <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-border/70 bg-muted/20 p-3 text-xs">
          <span className="text-muted-foreground">负责人</span>
          <span className="text-foreground">
            {displayUserName(linear.assignee)}
          </span>
          <span className="text-muted-foreground">创建者</span>
          <span className="text-foreground">
            {displayUserName(linear.creator)}
          </span>
          <span className="text-muted-foreground">团队</span>
          <span className="text-foreground">
            {linear.team?.name ?? linear.team?.key ?? "—"}
          </span>
          <span className="text-muted-foreground">项目</span>
          <span className="text-foreground">{linear.project?.name ?? "—"}</span>
          <span className="text-muted-foreground">周期</span>
          <span className="text-foreground">{linear.cycle?.name ?? "—"}</span>
          <span className="text-muted-foreground">估分</span>
          <span className="text-foreground">{linear.estimate ?? "—"}</span>
          <span className="text-muted-foreground">截止</span>
          <span className="text-foreground">{linear.due_date || "—"}</span>
        </div>
      ) : null}
      {linear?.relations?.length ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {linear.relations.map((relation) => (
            <span
              key={`${relation.direction}-${relation.type}-${relation.issue.id}`}
              className="rounded-md border border-border/70 px-2 py-1 text-xs text-muted-foreground"
            >
              {relation.direction === "inverse" ? "被" : ""}
              {relation.type}:{" "}
              {relation.issue.identifier ||
                relation.issue.title ||
                relation.issue.id}
            </span>
          ))}
        </div>
      ) : null}
      {!linear && issue?.url ? (
        <p className="text-xs text-muted-foreground">{issue.url}</p>
      ) : null}
    </DetailSection>
  );
}

function TimelineSection({
  timeline,
  loading,
  error,
}: {
  timeline: WorkflowTimelineSnapshot | null;
  loading: boolean;
  error: string | null;
}) {
  const nodeSync = new Map<string, NodeSyncState>();
  for (const state of timeline?.node_sync_states ?? []) {
    nodeSync.set(`${state.run_id}:${state.node_id}:${state.target}`, state);
  }

  return (
    <DetailSection label={zhCN.detail.workflowTimeline}>
      {loading ? (
        <p className="text-xs text-muted-foreground">
          {zhCN.detail.loadingTimeline}
        </p>
      ) : null}
      {error ? (
        <InlineError message={zhCN.detail.timelineLoadFailed(error)} />
      ) : null}
      {!loading && !error && timeline && timeline.nodes.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {zhCN.detail.noTimeline}
        </p>
      ) : null}
      {timeline?.nodes?.length ? (
        <div className="space-y-2">
          {timeline.nodes.map((node) => (
            <TimelineRow
              key={`${node.run_id}-${node.node_id}-${node.attempt ?? 0}`}
              node={node}
              syncStates={nodeSync}
            />
          ))}
        </div>
      ) : null}
    </DetailSection>
  );
}

function TimelineRow({
  node,
  syncStates,
}: {
  node: WorkflowNodeSummary;
  syncStates: Map<string, NodeSyncState>;
}) {
  const syncBadges = Array.from(syncStates.values()).filter(
    (state) => state.run_id === node.run_id && state.node_id === node.node_id,
  );
  return (
    <div className="rounded-lg border border-border/70 bg-card/70 px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-foreground">
          {node.title || node.node_id}
        </span>
        <StatusBadge
          variant={node.status === "failed" ? "destructive" : "muted"}
        >
          {node.status}
        </StatusBadge>
        {syncBadges.length === 0 ? (
          <StatusBadge variant="muted">sync pending</StatusBadge>
        ) : null}
        {syncBadges.map((state) => (
          <StatusBadge
            key={`${state.target}-${state.status}`}
            variant={state.status === "failed" ? "destructive" : "default"}
          >
            {state.target}: {state.status}
          </StatusBadge>
        ))}
      </div>
      {node.summary || node.body ? (
        <p className="mt-2 whitespace-normal break-words text-xs leading-relaxed text-muted-foreground">
          {node.summary || node.body}
        </p>
      ) : null}
      {node.completed_at ? (
        <p className="mt-2 text-xs text-muted-foreground">
          {formatRelativeTime(node.completed_at)}
        </p>
      ) : null}
    </div>
  );
}

function InlineError({ message }: { message: string }) {
  return (
    <p
      className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive"
      role="alert"
    >
      {message}
    </p>
  );
}

function StatusBadge({
  children,
  variant = "default",
}: {
  children: ReactNode;
  variant?: "default" | "destructive" | "muted";
}) {
  const cls = {
    default: "border-border/80 bg-muted/60 text-foreground",
    destructive: "border-destructive/50 bg-destructive/10 text-destructive",
    muted: "border-border/50 bg-muted/40 text-muted-foreground",
  }[variant];
  return (
    <span
      className={`inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-medium ${cls}`}
    >
      {children}
    </span>
  );
}

function DetailSection({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        {label}
      </p>
      {children}
    </div>
  );
}
