import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen, within } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { WorkerState } from '../types'
import { WorkerTable } from './WorkerTable'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('WorkerTable', () => {
  it('renders empty state when there are no workers', () => {
    render(<WorkerTable workers={[]} />)
    expectInDocument(screen.getByText('No workers'))
  })

  it('renders workers sorted by status with busy first', () => {
    const workers: WorkerState[] = [
      {
        id: 'idle-worker',
        agent_type: 'executor',
        status: 'idle',
        work_dir: '/tmp/idle',
        started_at: '2026-03-05T10:00:00.000Z',
        last_heartbeat: '2026-03-05T10:01:00.000Z',
      },
      {
        id: 'busy-worker',
        agent_type: 'executor',
        status: 'busy',
        current_task: 'important-task',
        work_dir: '/tmp/busy',
        started_at: '2026-03-05T10:00:00.000Z',
        last_heartbeat: '2026-03-05T10:01:00.000Z',
      },
    ]

    render(<WorkerTable workers={workers} />)

    const table = screen.getByRole('table', { name: 'Worker status' })
    const rows = within(table).getAllByRole('row')

    expect((rows[1].textContent ?? '').includes('busy-worker')).toBeTrue()
    expect((rows[2].textContent ?? '').includes('idle-worker')).toBeTrue()
  })

  it('truncates long IDs and current task text', () => {
    const workers: WorkerState[] = [
      {
        id: 'worker-id-abcdefghijklmnopqrstuvwxyz',
        agent_type: 'executor',
        status: 'busy',
        current_task: 'this-is-a-very-long-task-name-that-needs-truncation',
        work_dir: '/tmp/worker',
        started_at: '2026-03-05T10:00:00.000Z',
        last_heartbeat: '2026-03-05T10:01:00.000Z',
      },
    ]

    render(<WorkerTable workers={workers} />)

    expectInDocument(screen.getByText('worker-id...'))
    expectInDocument(screen.getByText('this-is-a-very-lo...'))
  })
})
