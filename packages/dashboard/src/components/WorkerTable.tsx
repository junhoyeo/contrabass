import type { WorkerState } from '../types'
import { formatElapsedSince, formatWorkerStatus } from '../i18n/format'
import { zhCN } from '../i18n/messages'
import './WorkerTable.css'

interface WorkerTableProps {
  workers: WorkerState[]
}

function formatAge(startedAt: string): string {
  return formatElapsedSince(startedAt)
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
    return <div className="worker-table__empty">{zhCN.workers.empty}</div>
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
      <table className="worker-table" aria-label={zhCN.workers.ariaLabel}>
        <thead>
          <tr>
            <th>{zhCN.workers.headers.workerID}</th>
            <th>{zhCN.workers.headers.status}</th>
            <th>{zhCN.workers.headers.currentTask}</th>
            <th>{zhCN.workers.headers.pid}</th>
            <th>{zhCN.workers.headers.age}</th>
          </tr>
        </thead>
        <tbody>
          {sortedWorkers.map((worker) => (
            <tr key={worker.id}>
              <td className="worker-table__mono" title={worker.id}>
                {truncate(worker.id, 12)}
              </td>
              <td>
                <span className={getStatusClass(worker.status)}>{formatWorkerStatus(worker.status)}</span>
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
