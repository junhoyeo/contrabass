import { useEffect, useMemo, useRef, useState } from "react";
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import type {
  BackoffEntry,
  DetailSelection,
  DetailSelectionKind,
  Issue,
  RunningEntry,
  SheetData,
  StateSnapshot,
} from "../types";
import type { QueueEventPayload } from "../hooks/useSSE";
import { AppSidebar, type QueueId } from "./AppSidebar";
import { IssueDataTable } from "./IssueDataTable";
import { IssueDetailSheet } from "./IssueDetailSheet";
import { QueuePanel } from "./QueuePanel";

interface AppLayoutProps {
  state: StateSnapshot;
  connected: boolean;
  runtimeLabel: string;
  queueEvents?: QueueEventPayload[];
}

interface QueueDef {
  id: QueueId;
  title: string;
  rows: RunningEntry[];
  emptyText: string;
}

export function getLinearState(issue: Issue): string | undefined {
  const meta = issue.tracker_meta as Record<string, unknown> | undefined;
  if (!meta) return undefined;
  const v = meta["linear_state"];
  return typeof v === "string" ? v : undefined;
}

function backoffAsRunningRow(entry: BackoffEntry, issue?: Issue): RunningEntry {
  return {
    issue_id: entry.issue_id,
    attempt: entry.attempt,
    pid: 0,
    session_id: "",
    workspace: "",
    started_at: entry.retry_at,
    phase: 0,
    tokens_in: 0,
    tokens_out: 0,
    last_activity_at: issue?.updated_at,
    last_activity_kind: "backoff_enqueued",
    diff_added: 0,
    diff_removed: 0,
    diff_files: 0,
    diff_status: "ok",
    phase_label: `attempt ${entry.attempt}`,
  };
}

function issueAsTodoRow(issue: Issue): RunningEntry {
  return {
    issue_id: issue.id,
    attempt: 0,
    pid: 0,
    session_id: issue.identifier ?? "",
    workspace: "",
    started_at: issue.updated_at ?? "",
    phase: 0,
    tokens_in: 0,
    tokens_out: 0,
    phase_label: issue.identifier,
    last_activity_kind: issue.title,
    diff_status: "ok",
  };
}

function queueIdToKind(qid: QueueId): DetailSelectionKind {
  switch (qid) {
    case "running":
      return "running";
    case "backoff":
      return "backoff";
    case "todo":
      return "todo";
    case "backlog":
      return "todo";
    case "recent_done":
      return "done";
    case "canceled":
      return "canceled";
  }
}

export function AppLayout({
  state,
  connected,
  runtimeLabel,
  queueEvents = [],
}: AppLayoutProps) {
  const [active, setActive] = useState<QueueId>("running");
  const [selection, setSelection] = useState<DetailSelection | null>(null);
  const lastKnownSheetRef = useRef<SheetData | null>(null);

  const issuesArray = useMemo<Issue[]>(
    () => Object.values(state.issues ?? {}),
    [state.issues],
  );

  const queues = useMemo<Record<QueueId, QueueDef>>(() => {
    const running = state.running ?? [];
    const backoff = state.backoff ?? [];
    const todo = issuesArray.filter((i) => getLinearState(i) === "Todo");
    const backlog = issuesArray.filter((i) => getLinearState(i) === "Backlog");
    const done = issuesArray.filter((i) => {
      const s = getLinearState(i);
      return s === "Done" || s === "Released";
    });
    const canceled = issuesArray.filter((i) => {
      const s = getLinearState(i);
      return s === "Canceled" || s === "Won't Do";
    });

    return {
      running: {
        id: "running",
        title: "运行中",
        rows: running,
        emptyText: "暂无运行中任务",
      },
      backoff: {
        id: "backoff",
        title: "退避队列",
        rows: backoff.map((b) =>
          backoffAsRunningRow(b, state.issues?.[b.issue_id]),
        ),
        emptyText: "退避队列为空",
      },
      todo: {
        id: "todo",
        title: "待办",
        rows: todo.map(issueAsTodoRow),
        emptyText: "Todo 列表为空",
      },
      backlog: {
        id: "backlog",
        title: "Backlog",
        rows: backlog.map(issueAsTodoRow),
        emptyText: "Backlog 列表为空",
      },
      recent_done: {
        id: "recent_done",
        title: "最近完成",
        rows: done.slice(0, 50).map(issueAsTodoRow),
        emptyText: "暂无近期完成任务",
      },
      canceled: {
        id: "canceled",
        title: "取消",
        rows: canceled.map(issueAsTodoRow),
        emptyText: "暂无取消任务",
      },
    };
  }, [state.running, state.backoff, state.issues, issuesArray]);

  const counts: Partial<Record<QueueId, number>> = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(queues).map(([k, q]) => [k, q.rows.length]),
      ) as Partial<Record<QueueId, number>>,
    [queues],
  );

  const sheetData = useMemo<SheetData | null>(() => {
    if (!selection) return null;
    const { kind, issueId } = selection;
    const issue = state.issues?.[issueId];
    if (kind === "running") {
      const running = state.running?.find((e) => e.issue_id === issueId);
      return { kind, issue, running, backoff: undefined };
    }
    if (kind === "backoff") {
      const backoff = state.backoff?.find((e) => e.issue_id === issueId);
      return { kind, issue, running: undefined, backoff };
    }
    return { kind, issue, running: undefined, backoff: undefined };
  }, [selection, state.running, state.backoff, state.issues]);

  useEffect(() => {
    if (sheetData) lastKnownSheetRef.current = sheetData;
  }, [sheetData]);

  const currentQueue = queues[active];
  const queuedTotal =
    (counts.backoff ?? 0) + (counts.todo ?? 0) + (counts.backlog ?? 0);
  const doneTotal = (counts.recent_done ?? 0) + (counts.canceled ?? 0);

  return (
    <SidebarProvider className="h-full min-h-0 bg-transparent">
      <AppSidebar
        active={active}
        onSelect={(id) => {
          setActive(id);
          setSelection(null);
        }}
        counts={counts}
        connected={connected}
        runtimeLabel={runtimeLabel}
      />
      <SidebarInset className="min-w-0 overflow-hidden">
        <header className="flex min-h-16 shrink-0 flex-wrap items-center gap-3 border-b border-border/70 bg-background/90 px-3 py-3 shadow-sm backdrop-blur md:px-5">
          <SidebarTrigger className="-ml-1 border border-border/70 bg-card/80 shadow-xs hover:bg-muted" />
          <Separator
            orientation="vertical"
            className="mr-1 hidden h-5 bg-border/70 sm:block"
          />
          <div className="min-w-0">
            <p className="text-[0.65rem] font-semibold uppercase tracking-[0.26em] text-primary/90">
              Control Queue
            </p>
            <h2 className="truncate text-lg font-semibold leading-tight text-foreground">
              {currentQueue.title}
            </h2>
          </div>
          <span className="ml-auto rounded-full border border-border/70 bg-card px-3 py-1 text-xs font-medium text-muted-foreground shadow-xs">
            {currentQueue.rows.length} 项
          </span>
          {state.build_info && state.build_info.commit ? (
            <span
              className="rounded-full border border-border/70 bg-card px-3 py-1 font-mono text-[10px] uppercase tracking-wide text-muted-foreground shadow-xs"
              title={`built ${state.build_info.date}`}
            >
              {state.build_info.version}@{state.build_info.commit}
            </span>
          ) : null}
        </header>
        <div className="min-h-0 flex-1 overflow-hidden p-3 sm:p-4 lg:p-6">
          <div className="mx-auto flex h-full max-w-[1600px] min-w-0 flex-col gap-4">
            <section
              className="grid grid-cols-2 gap-3 lg:grid-cols-4"
              aria-label="队列摘要"
            >
              <OverviewCard
                label="连接"
                value={connected ? "在线" : "离线"}
                tone={connected ? "live" : "warn"}
              />
              <OverviewCard
                label="运行负载"
                value={`${state.stats.Running}/${state.stats.MaxAgents}`}
              />
              <OverviewCard label="待处理" value={queuedTotal} />
              <OverviewCard
                label="归档"
                value={doneTotal}
                subtle={runtimeLabel}
              />
            </section>

            <QueuePanel events={queueEvents} />

            <section className="min-h-0 flex-1 overflow-hidden rounded-2xl border border-border/70 bg-card/80 shadow-lg ring-1 ring-white/5">
              <IssueDataTable
                entries={currentQueue.rows}
                emptyText={currentQueue.emptyText}
                onSelect={(entry) =>
                  setSelection({
                    kind: queueIdToKind(active),
                    issueId: entry.issue_id,
                  })
                }
                selectedId={selection?.issueId ?? null}
              />
            </section>
          </div>
        </div>
      </SidebarInset>
      <IssueDetailSheet
        data={selection ? (sheetData ?? lastKnownSheetRef.current) : null}
        isStale={!!selection && !sheetData}
        onOpenChange={(open) => {
          if (!open) setSelection(null);
        }}
      />
    </SidebarProvider>
  );
}

function OverviewCard({
  label,
  value,
  subtle,
  tone,
}: {
  label: string;
  value: string | number;
  subtle?: string;
  tone?: "live" | "warn";
}) {
  return (
    <div className="relative overflow-hidden rounded-2xl border border-border/70 bg-card/80 px-4 py-3 shadow-sm">
      <div
        className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-primary/70 to-transparent"
        aria-hidden
      />
      <p className="text-[0.68rem] font-semibold uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </p>
      <div className="mt-2 flex items-end justify-between gap-2">
        <p className="font-mono text-2xl font-semibold leading-none tabular-nums text-foreground">
          {value}
        </p>
        {tone ? (
          <span
            className={`mb-1 h-2.5 w-2.5 rounded-full ${tone === "live" ? "bg-accent shadow-[0_0_18px_var(--accent)]" : "bg-destructive"}`}
            aria-hidden
          />
        ) : null}
      </div>
      {subtle ? (
        <p className="mt-2 truncate font-mono text-xs text-muted-foreground">
          {subtle}
        </p>
      ) : null}
    </div>
  );
}
