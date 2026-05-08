import type { RunningEntry } from '../types'
import { formatElapsedSince, formatNumber, formatPhase, formatRelativeTime } from '../i18n/format'
import { zhCN } from '../i18n/messages'
import './SessionsTable.css'

interface SessionsTableProps {
  entries: RunningEntry[]
}

function formatAge(startedAt: string): string {
  return formatElapsedSince(startedAt)
}

function truncateSessionID(sessionID: string): string {
  return sessionID.slice(0, 8)
}

function phaseLabel(entry: RunningEntry): string {
  const serverLabel = entry.phase_label?.trim()
  if (serverLabel) {
    return serverLabel
  }

  return formatPhase(entry.phase)
}

function activityTone(lastActivityAt: string | undefined): 'none' | 'fresh' | 'warm' | 'stale' {
  if (!lastActivityAt) {
    return 'none'
  }

  const parsed = Date.parse(lastActivityAt)
  if (Number.isNaN(parsed)) {
    return 'none'
  }

  const ageSeconds = Math.floor(Math.max(0, Date.now() - parsed) / 1000)
  if (ageSeconds < 30) {
    return 'fresh'
  }
  if (ageSeconds <= 180) {
    return 'warm'
  }
  return 'stale'
}

function activityDotColor(tone: ReturnType<typeof activityTone>): string {
  switch (tone) {
    case 'fresh':
      return 'oklch(70% 0.18 145)'
    case 'warm':
      return 'oklch(76% 0.15 80)'
    case 'stale':
      return 'oklch(63% 0.2 28)'
    default:
      return 'oklch(62% 0.02 250)'
  }
}

function renderLastActivity(entry: RunningEntry) {
  const tone = activityTone(entry.last_activity_at)
  const relative = entry.last_activity_at ? formatRelativeTime(entry.last_activity_at) : zhCN.sessions.relative.unknown

  return (
    <span
      className="sessions-table__activity"
      data-activity-tone={tone}
      title={entry.last_heartbeat_at ? `heartbeat ${formatRelativeTime(entry.last_heartbeat_at)}` : undefined}
    >
      <span
        aria-hidden="true"
        className="sessions-table__activity-dot"
        style={{
          backgroundColor: activityDotColor(tone),
          borderRadius: '999px',
          display: 'inline-block',
          height: '0.55rem',
          marginRight: '0.35rem',
          width: '0.55rem',
        }}
      />
      <span>{relative}</span>
      {entry.last_activity_kind ? (
        <span className="sessions-table__activity-kind" style={{ color: 'var(--text-secondary)', marginLeft: '0.35rem' }}>
          {entry.last_activity_kind}
        </span>
      ) : null}
    </span>
  )
}

function renderDiff(entry: RunningEntry): string {
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

  return `+${added} -${removed} (${files} files)`
}

function renderIteration(entry: RunningEntry) {
  const max = entry.iteration_max ?? 0
  if (max <= 0) {
    return null
  }

  return (
    <span
      className="sessions-table__iter-badge"
      style={{
        border: '1px solid var(--border-color)',
        borderRadius: '999px',
        padding: '0.1rem 0.45rem',
      }}
    >
      iter {entry.iteration ?? 0}/{max}
    </span>
  )
}

// StagePill renders a 5-step pill showing the current agent stage.
// When agent_stage is empty/step is 0, falls back to the phase_label text.
function StagePill({ entry }: { entry: RunningEntry }) {
  const stage = entry.agent_stage ?? ''
  const step = entry.agent_stage_step ?? 0

  if (!stage || step === 0) {
    return <span title={phaseLabel(entry)}>{phaseLabel(entry)}</span>
  }

  const stageLabel = zhCN.sessions.stages[step - 1] ?? stage

  return (
    <span className="sessions-table__stage-pill" aria-label={stage}>
      <span
        className="sessions-table__stage-name"
        style={{ color: 'var(--text-secondary)', display: 'block', fontSize: '0.7rem', marginBottom: '0.2rem' }}
      >
        {stageLabel}
      </span>
      <span style={{ display: 'flex', gap: '0.2rem' }}>
        {[1, 2, 3, 4, 5].map((s) => {
          let bg: string
          let border: string
          if (s < step) {
            bg = 'var(--accent-color, oklch(55% 0.18 250))'
            border = 'var(--accent-color, oklch(55% 0.18 250))'
          } else if (s === step) {
            bg = 'var(--accent-active, oklch(45% 0.22 250))'
            border = 'var(--accent-active, oklch(45% 0.22 250))'
          } else {
            bg = 'transparent'
            border = 'var(--border-color, oklch(70% 0.02 250))'
          }
          return (
            <span
              key={s}
              aria-hidden="true"
              style={{
                background: bg,
                border: `1.5px solid ${border}`,
                borderRadius: '2px',
                display: 'inline-block',
                height: '0.55rem',
                width: '1rem',
              }}
            />
          )
        })}
      </span>
    </span>
  )
}

// renderDoneBy renders the "Done by" cell per design.md Decision 5:
// never shows a countdown, only a clock time (~HH:MM) or elapsed fallback.
function renderDoneBy(entry: RunningEntry): string {
  const etaAt = entry.eta_completion_at ?? ''
  const conf = entry.eta_confidence ?? ''

  if (etaAt && (conf === 'medium' || conf === 'high')) {
    const date = new Date(etaAt)
    if (!Number.isNaN(date.getTime())) {
      const hh = date.getHours().toString().padStart(2, '0')
      const mm = date.getMinutes().toString().padStart(2, '0')
      return `~${hh}:${mm}`
    }
  }

  if (conf === 'low') {
    const startedAt = entry.started_at ? Date.parse(entry.started_at) : NaN
    if (!Number.isNaN(startedAt)) {
      const elapsedSeconds = Math.floor(Math.max(0, Date.now() - startedAt) / 1000)
      if (elapsedSeconds >= 60) {
        const minutes = Math.floor(elapsedSeconds / 60)
        return zhCN.sessions.elapsedNormal(minutes)
      }
    }
  }

  return '—'
}

export function SessionsTable({ entries }: SessionsTableProps) {
  if (entries.length === 0) {
    return <div className="sessions-table__empty">{zhCN.sessions.empty}</div>
  }

  const sortedEntries = [...entries].sort(
    (a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
  )

  return (
    <div className="sessions-table__wrapper">
      <table className="sessions-table" aria-label={zhCN.sessions.ariaLabel}>
        <thead>
          <tr>
            <th>{zhCN.sessions.headers.issueID}</th>
            <th>{zhCN.sessions.headers.phase}</th>
            <th>{zhCN.sessions.headers.lastActivity}</th>
            <th>{zhCN.sessions.headers.diff}</th>
            <th>{zhCN.sessions.headers.doneBy}</th>
            <th>{zhCN.sessions.headers.iter}</th>
            <th>{zhCN.sessions.headers.pid}</th>
            <th>{zhCN.sessions.headers.age}</th>
            <th>{zhCN.sessions.headers.turns}</th>
            <th>{zhCN.sessions.headers.tokensIn}</th>
            <th>{zhCN.sessions.headers.tokensOut}</th>
            <th>{zhCN.sessions.headers.sessionID}</th>
            <th>{zhCN.sessions.headers.lastEvent}</th>
          </tr>
        </thead>
        <tbody>
          {sortedEntries.map((entry) => (
            <tr key={`${entry.issue_id}-${entry.pid}-${entry.started_at}`}>
              <td>{entry.issue_id}</td>
              <td><StagePill entry={entry} /></td>
              <td>{renderLastActivity(entry)}</td>
              <td className="sessions-table__mono" title={entry.diff_status && entry.diff_status !== 'ok' ? entry.diff_status : undefined}>
                {renderDiff(entry)}
              </td>
              <td className="sessions-table__mono">{renderDoneBy(entry)}</td>
              <td>{renderIteration(entry)}</td>
              <td className="sessions-table__mono">{entry.pid}</td>
              <td>{formatAge(entry.started_at)}</td>
              <td className="sessions-table__mono">{entry.attempt}</td>
              <td className="sessions-table__mono">{formatNumber(entry.tokens_in)}</td>
              <td className="sessions-table__mono">{formatNumber(entry.tokens_out)}</td>
              <td className="sessions-table__mono" title={entry.session_id}>
                {truncateSessionID(entry.session_id)}
              </td>
              <td className="sessions-table__last-event">-</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default SessionsTable
