import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { TeamSnapshot } from '../types'
import { TeamTable } from './TeamTable'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

function expectHasClass(value: unknown, className: string) {
  ;(expect(value) as any).toHaveClass(className)
}

afterEach(() => {
  cleanup()
})

function makeSnapshot(phase: string): TeamSnapshot {
  return {
    name: 'team-alpha',
    phase: {
      phase,
      fix_loop_count: 1,
      transitions: [],
      artifacts: {},
    },
    workers: [],
    tasks: [],
    config: {
      max_workers: 4,
      max_fix_loops: 2,
      claim_lease_seconds: 120,
      state_dir: '/tmp/team',
      agent_type: 'executor',
    },
    created_at: '2026-03-05T10:00:00.000Z',
  }
}

describe('TeamTable', () => {
  it('renders empty state for null snapshot', () => {
    render(<TeamTable snapshot={null} />)
    expectInDocument(screen.getByText('暂无运行团队'))
  })

  it('renders snapshot with no workers and no tasks', () => {
    render(<TeamTable snapshot={makeSnapshot('team-plan')} />)

    expectInDocument(screen.getByText('team-alpha'))
    expectInDocument(screen.getByText('0/0'))
    expectInDocument(screen.getByText('0/0/0'))
  })

  it('renders workers/tasks counters from snapshot data', () => {
    const snapshot = makeSnapshot('team-exec')
    snapshot.workers = [
      {
        id: 'worker-busy',
        agent_type: 'executor',
        status: 'busy',
        current_task: 'task-1',
        work_dir: '/tmp/w1',
        started_at: '2026-03-05T10:00:00.000Z',
        last_heartbeat: '2026-03-05T10:02:00.000Z',
      },
      {
        id: 'worker-idle',
        agent_type: 'executor',
        status: 'idle',
        work_dir: '/tmp/w2',
        started_at: '2026-03-05T10:00:00.000Z',
        last_heartbeat: '2026-03-05T10:02:00.000Z',
      },
    ]
    snapshot.tasks = [
      {
        id: 'task-1',
        subject: 'Task one',
        description: 'one',
        status: 'completed',
        version: 1,
        created_at: '2026-03-05T10:00:00.000Z',
        updated_at: '2026-03-05T10:01:00.000Z',
      },
      {
        id: 'task-2',
        subject: 'Task two',
        description: 'two',
        status: 'failed',
        version: 1,
        created_at: '2026-03-05T10:00:00.000Z',
        updated_at: '2026-03-05T10:01:00.000Z',
      },
    ]

    render(<TeamTable snapshot={snapshot} />)

    expectInDocument(screen.getByText('执行'))
    expectInDocument(screen.getByText('1/2'))
    expectInDocument(screen.getByText('1/2/1'))
  })

  it('applies phase badge classes for phase color variants', () => {
    const { rerender } = render(<TeamTable snapshot={makeSnapshot('team-plan')} />)
    expectHasClass(screen.getByText('规划'), 'team-table__phase-badge--plan')

    rerender(<TeamTable snapshot={makeSnapshot('team-exec')} />)
    expectHasClass(screen.getByText('执行'), 'team-table__phase-badge--exec')

    rerender(<TeamTable snapshot={makeSnapshot('team-verify')} />)
    expectHasClass(screen.getByText('验证'), 'team-table__phase-badge--verify')

    rerender(<TeamTable snapshot={makeSnapshot('team-fix')} />)
    expectHasClass(screen.getByText('修复'), 'team-table__phase-badge--fix')

    rerender(<TeamTable snapshot={makeSnapshot('failed')} />)
    expectHasClass(screen.getByText('失败'), 'team-table__phase-badge--failed')
  })
})
