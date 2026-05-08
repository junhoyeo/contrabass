import type { TeamSnapshot, TeamTask } from '../types'
import { formatElapsedSince, formatTeamPhase } from '../i18n/format'
import { zhCN } from '../i18n/messages'
import './TeamTable.css'

interface TeamTableProps {
  snapshot: TeamSnapshot | null
}

function formatAge(createdAt: string): string {
  return formatElapsedSince(createdAt)
}

function getPhaseBadgeClass(phase: string): string {
  switch (phase) {
    case 'team-plan':
    case 'team-prd':
      return 'team-table__phase-badge--plan'
    case 'team-exec':
      return 'team-table__phase-badge--exec'
    case 'team-verify':
    case 'complete':
      return 'team-table__phase-badge--verify'
    case 'team-fix':
      return 'team-table__phase-badge--fix'
    case 'failed':
    case 'cancelled':
      return 'team-table__phase-badge--failed'
    default:
      return 'team-table__phase-badge--unknown'
  }
}

function isTaskCompleted(task: TeamTask): boolean {
  const status = task.status.toLowerCase()
  return status === 'complete' || status === 'completed' || status === 'done' || status === 'succeeded'
}

function isTaskFailed(task: TeamTask): boolean {
  const status = task.status.toLowerCase()
  return status === 'failed' || status === 'cancelled' || status === 'canceled'
}

export function TeamTable({ snapshot }: TeamTableProps) {
  if (snapshot === null) {
    return <div className="team-table__empty">{zhCN.team.empty}</div>
  }

  const activeWorkers = snapshot.workers.filter((worker) => worker.status.toLowerCase() === 'busy').length
  const completedTasks = snapshot.tasks.filter(isTaskCompleted).length
  const failedTasks = snapshot.tasks.filter(isTaskFailed).length

  return (
    <section className="team-table__section" aria-label={zhCN.team.ariaLabel}>
      <header className="team-table__header">
        <h3 className="team-table__name">{snapshot.name}</h3>
        <p className="team-table__config">
          {zhCN.team.config(
            snapshot.config.agent_type,
            snapshot.config.max_workers,
            snapshot.config.max_fix_loops,
          )}
        </p>
      </header>

      <div className="team-table__wrapper">
        <table className="team-table" aria-label={zhCN.team.tableAriaLabel}>
          <thead>
            <tr>
              <th>{zhCN.team.headers.phase}</th>
              <th>{zhCN.team.headers.workers}</th>
              <th>{zhCN.team.headers.tasks}</th>
              <th>{zhCN.team.headers.fixLoops}</th>
              <th>{zhCN.team.headers.age}</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>
                <span className={`team-table__phase-badge ${getPhaseBadgeClass(snapshot.phase.phase)}`}>
                  {formatTeamPhase(snapshot.phase.phase)}
                </span>
              </td>
              <td className="team-table__mono">
                {activeWorkers}/{snapshot.workers.length}
              </td>
              <td className="team-table__mono">
                {completedTasks}/{snapshot.tasks.length}/{failedTasks}
              </td>
              <td className="team-table__mono">{snapshot.phase.fix_loop_count}</td>
              <td>{formatAge(snapshot.created_at)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  )
}

export default TeamTable
