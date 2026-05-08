import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { RunningEntry } from '../types'
import { formatElapsedSince, formatNumber, formatRelativeTime } from '../i18n/format'

interface IssueDataTableProps {
  entries: RunningEntry[]
  emptyText: string
  onSelect: (entry: RunningEntry) => void
  selectedId: string | null
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

export function diffSummary(entry: RunningEntry): string {
  const status = entry.diff_status ?? 'ok'
  if (status !== 'ok') {
    return '?'
  }
  const added = entry.diff_added ?? 0
  const removed = entry.diff_removed ?? 0
  const files = entry.diff_files ?? 0
  if (added === 0 && removed === 0 && files === 0) {
    return '—'
  }
  return `+${added} -${removed} (${files})`
}

function activityTone(at: string | undefined): 'fresh' | 'warm' | 'stale' | 'none' {
  if (!at) return 'none'
  const parsed = Date.parse(at)
  if (Number.isNaN(parsed)) return 'none'
  const ageSeconds = Math.floor(Math.max(0, Date.now() - parsed) / 1000)
  if (ageSeconds < 30) return 'fresh'
  if (ageSeconds <= 180) return 'warm'
  return 'stale'
}

function dotColor(tone: 'fresh' | 'warm' | 'stale' | 'none'): string {
  switch (tone) {
    case 'fresh': return 'bg-emerald-500'
    case 'warm':  return 'bg-amber-500'
    case 'stale': return 'bg-rose-500'
    default:      return 'bg-muted'
  }
}

function entryKey(entry: RunningEntry): string {
  return `${entry.issue_id}-${entry.pid}-${entry.started_at}`
}

export function IssueDataTable({ entries, emptyText, onSelect, selectedId }: IssueDataTableProps) {
  if (entries.length === 0) {
    return (
      <div className="flex min-h-56 items-center justify-center p-4">
        <div className="w-full rounded-2xl border border-dashed border-border/80 bg-background/40 px-6 py-12 text-center text-sm text-muted-foreground">
          {emptyText}
        </div>
      </div>
    )
  }

  return (
    <div className="h-full min-h-0 overflow-auto">
      <div className="grid gap-3 p-3 pb-6 lg:hidden">
        {entries.map((entry) => {
          const tone = activityTone(entry.last_activity_at)
          const isSelected = entry.issue_id === selectedId

          return (
            <button
              key={entryKey(entry)}
              type="button"
              onClick={() => onSelect(entry)}
              className={`rounded-2xl border p-4 text-left shadow-sm transition active:scale-[0.99] ${
                isSelected
                  ? 'border-primary/70 bg-primary/10'
                  : 'border-border/70 bg-background/40 hover:border-primary/50 hover:bg-muted/50'
              }`}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-mono text-xs uppercase tracking-[0.16em] text-primary">{shortId(entry.issue_id)}</p>
                  <p className="mt-1 truncate text-base font-semibold text-foreground">{entry.phase_label ?? '—'}</p>
                </div>
                <span className={`mt-1 h-2.5 w-2.5 shrink-0 rounded-full ${dotColor(tone)}`} aria-hidden />
              </div>

              <div className="mt-4 grid grid-cols-2 gap-2 text-xs">
                <MobileFact label="活动" value={entry.last_activity_at ? formatRelativeTime(entry.last_activity_at) : '—'} />
                <MobileFact label="已运行" value={formatElapsedSince(entry.started_at)} mono />
                <MobileFact label="差异" value={diffSummary(entry)} mono />
                <MobileFact label="Token" value={`${formatNumber(entry.tokens_in)} / ${formatNumber(entry.tokens_out)}`} mono />
              </div>

              {entry.last_activity_kind ? (
                <p className="mt-3 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{entry.last_activity_kind}</p>
              ) : null}
            </button>
          )
        })}
      </div>

      <div className="hidden min-w-0 lg:block">
        <Table className="min-w-[960px]">
          <TableHeader className="sticky top-0 z-10 bg-card/95 backdrop-blur">
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-28 px-4 text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">ID</TableHead>
              <TableHead className="w-44 text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">阶段</TableHead>
              <TableHead className="min-w-[22rem] text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">上次活动</TableHead>
              <TableHead className="w-36 text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">差异</TableHead>
              <TableHead className="w-28 text-right text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">已运行</TableHead>
              <TableHead className="w-32 text-right text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">输入 Token</TableHead>
              <TableHead className="w-32 pr-4 text-right text-[0.68rem] uppercase tracking-[0.16em] text-muted-foreground">输出 Token</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((entry) => {
              const tone = activityTone(entry.last_activity_at)
              const isSelected = entry.issue_id === selectedId
              return (
                <TableRow
                  key={entryKey(entry)}
                  data-state={isSelected ? 'selected' : undefined}
                  onClick={() => onSelect(entry)}
                  className="cursor-pointer"
                >
                  <TableCell className="px-4 font-mono text-xs text-primary">{shortId(entry.issue_id)}</TableCell>
                  <TableCell className="max-w-44 truncate text-sm font-medium">{entry.phase_label ?? '—'}</TableCell>
                  <TableCell className="text-sm">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className={`inline-block h-2.5 w-2.5 shrink-0 rounded-full ${dotColor(tone)}`} aria-hidden />
                      <span className="shrink-0 font-medium">
                        {entry.last_activity_at ? formatRelativeTime(entry.last_activity_at) : '—'}
                      </span>
                      {entry.last_activity_kind ? (
                        <span className="truncate text-xs text-muted-foreground">{entry.last_activity_kind}</span>
                      ) : null}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{diffSummary(entry)}</TableCell>
                  <TableCell className="font-mono text-xs text-right">
                    {formatElapsedSince(entry.started_at)}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-right">
                    {formatNumber(entry.tokens_in)}
                  </TableCell>
                  <TableCell className="pr-4 font-mono text-xs text-right">
                    {formatNumber(entry.tokens_out)}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

function MobileFact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-xl border border-border/60 bg-card/70 px-3 py-2">
      <p className="text-[0.65rem] uppercase tracking-[0.14em] text-muted-foreground">{label}</p>
      <p className={`mt-1 truncate text-sm text-foreground ${mono ? 'font-mono tabular-nums' : 'font-medium'}`}>{value}</p>
    </div>
  )
}
