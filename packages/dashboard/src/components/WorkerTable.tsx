import type { WorkerState } from '../types'
import './WorkerTable.css'

interface WorkerTableProps {
  workers: WorkerState[]
}

function formatAge(startedAt: string): string {
  const started = Date.parse(startedAt)
  if (Number.isNaN(started)) {
    return '-'
  }

  const elapsedMs = Math.max(0, Date.now() - started)
  const totalSeconds = Math.floor(elapsedMs / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}m ${seconds}s`
}

function truncate(value: string, limit: number): string {
  if (value.length <= limit) {
    return value
  }

  return `${value.slice(0, limit - 3)}...`
}

function getStatusClass(status: string): string {
  switch (status.toLowerCase()) {
    case 'busy':
      return 'worker-table__status worker-table__status--busy'
    case 'stopped':
      return 'worker-table__status worker-table__status--stopped'
    default:
      return 'worker-table__status worker-table__status--idle'
  }
}

function getStatusOrder(status: string): number {
  switch (status.toLowerCase()) {
    case 'busy':
      return 0
    case 'idle':
      return 1
    default:
      return 2
  }
}

export function WorkerTable({ workers }: WorkerTableProps) {
  if (workers.length === 0) {
    return <div className="worker-table__empty">No workers</div>
  }

  const sortedWorkers = [...workers].sort((a, b) => {
    const statusOrder = getStatusOrder(a.status) - getStatusOrder(b.status)
    if (statusOrder !== 0) {
      return statusOrder
    }

    return a.id.localeCompare(b.id)
  })

  return (
    <div className="worker-table__wrapper">
      <table className="worker-table" aria-label="Worker status">
        <thead>
          <tr>
            <th>Worker ID</th>
            <th>Status</th>
            <th>Current Task</th>
            <th>PID</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {sortedWorkers.map((worker) => (
            <tr key={worker.id}>
              <td className="worker-table__mono" title={worker.id}>
                {truncate(worker.id, 12)}
              </td>
              <td>
                <span className={getStatusClass(worker.status)}>{worker.status}</span>
              </td>
              <td title={worker.current_task ?? '-'}>{truncate(worker.current_task ?? '-', 20)}</td>
              <td className="worker-table__mono">{worker.pid ?? '-'}</td>
              <td>{formatAge(worker.started_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default WorkerTable
