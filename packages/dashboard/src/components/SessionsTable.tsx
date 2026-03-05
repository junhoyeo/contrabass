import type { RunningEntry } from '../types'
import './SessionsTable.css'

interface SessionsTableProps {
  entries: RunningEntry[]
}

const PHASE_NAMES: Record<number, string> = {
  0: 'PreparingWorkspace',
  1: 'BuildingPrompt',
  2: 'LaunchingAgentProcess',
  3: 'InitializingSession',
  4: 'StreamingTurn',
  5: 'Finishing',
  6: 'Succeeded',
  7: 'Failed',
  8: 'TimedOut',
  9: 'Stalled',
  10: 'CanceledByReconciliation',
}

function formatAge(startedAt: string): string {
  const started = new Date(startedAt).getTime()
  if (Number.isNaN(started)) {
    return '-'
  }

  const elapsedMs = Math.max(0, Date.now() - started)
  const totalSeconds = Math.floor(elapsedMs / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60

  return `${minutes}m ${seconds}s`
}

function formatPhase(phase: number): string {
  return PHASE_NAMES[phase] ?? `Unknown(${phase})`
}

function truncateSessionID(sessionID: string): string {
  return sessionID.slice(0, 8)
}

export function SessionsTable({ entries }: SessionsTableProps) {
  if (entries.length === 0) {
    return <div className="sessions-table__empty">No running sessions</div>
  }

  const sortedEntries = [...entries].sort(
    (a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
  )

  return (
    <div className="sessions-table__wrapper">
      <table className="sessions-table" aria-label="Running sessions">
        <thead>
          <tr>
            <th>Issue ID</th>
            <th>Stage/Phase</th>
            <th>PID</th>
            <th>Age</th>
            <th>Turns</th>
            <th>Tokens In</th>
            <th>Tokens Out</th>
            <th>Session ID</th>
            <th>Last Event</th>
          </tr>
        </thead>
        <tbody>
          {sortedEntries.map((entry) => (
            <tr key={`${entry.issue_id}-${entry.pid}-${entry.started_at}`}>
              <td>{entry.issue_id}</td>
              <td title={formatPhase(entry.phase)}>{formatPhase(entry.phase)}</td>
              <td className="sessions-table__mono">{entry.pid}</td>
              <td>{formatAge(entry.started_at)}</td>
              <td className="sessions-table__mono">{entry.attempt}</td>
              <td className="sessions-table__mono">{entry.tokens_in.toLocaleString()}</td>
              <td className="sessions-table__mono">{entry.tokens_out.toLocaleString()}</td>
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
