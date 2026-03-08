import type { TeamSnapshot, TeamTask } from '../types'
import './TeamTable.css'

interface TeamTableProps {
  snapshot: TeamSnapshot | null
}

const PHASE_LABELS: Record<string, string> = {
  'team-plan': 'Plan',
  'team-prd': 'PRD',
  'team-exec': 'Exec',
  'team-verify': 'Verify',
  'team-fix': 'Fix',
  complete: 'Done',
  failed: 'Failed',
  cancelled: 'Cancel',
}

function formatAge(createdAt: string): string {
  const created = Date.parse(createdAt)
  if (Number.isNaN(created)) {
    return '-'
  }

  const elapsedMs = Math.max(0, Date.now() - created)
  const totalSeconds = Math.floor(elapsedMs / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}m ${seconds}s`
}

function getPhaseLabel(phase: string): string {
  return PHASE_LABELS[phase] ?? phase
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
    return <div className="team-table__empty">No team running</div>
  }

  const activeWorkers = snapshot.workers.filter((worker) => worker.status.toLowerCase() === 'busy').length
  const completedTasks = snapshot.tasks.filter(isTaskCompleted).length
  const failedTasks = snapshot.tasks.filter(isTaskFailed).length

  return (
    <section className="team-table__section" aria-label="Team status">
      <header className="team-table__header">
        <h3 className="team-table__name">{snapshot.name}</h3>
        <p className="team-table__config">
          Agent {snapshot.config.agent_type} · Workers {snapshot.config.max_workers} · Max fix loops{' '}
          {snapshot.config.max_fix_loops}
        </p>
      </header>

      <div className="team-table__wrapper">
        <table className="team-table" aria-label="Team status table">
          <thead>
            <tr>
              <th>Phase</th>
              <th>Workers</th>
              <th>Tasks</th>
              <th>Fix Loops</th>
              <th>Age</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td>
                <span className={`team-table__phase-badge ${getPhaseBadgeClass(snapshot.phase.phase)}`}>
                  {getPhaseLabel(snapshot.phase.phase)}
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
