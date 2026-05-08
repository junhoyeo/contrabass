import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen, within } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { RunningEntry } from '../types'
import { SessionsTable } from './SessionsTable'

type TableEntry = RunningEntry & {
  phase_label?: string
  last_activity_at?: string
  last_activity_kind?: string
  iteration?: number
  iteration_max?: number
  diff_added?: number
  diff_removed?: number
  diff_files?: number
  diff_status?: string
}

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

function createEntry(overrides: Partial<TableEntry> = {}): TableEntry {
  return {
    issue_id: 'ISSUE-100',
    attempt: 1,
    pid: 1234,
    session_id: 'abcdef1234567890',
    workspace: '/tmp/one',
    started_at: '2026-03-05T10:00:00.000Z',
    phase: 4,
    tokens_in: 1500,
    tokens_out: 500,
    ...overrides,
  }
}

afterEach(() => {
  cleanup()
})

describe('SessionsTable', () => {
  it('renders rows for each running entry with issue IDs', () => {
    const entries: RunningEntry[] = [
      createEntry({
        issue_id: 'ISSUE-100',
        attempt: 2,
        pid: 1234,
        session_id: 'abcdef1234567890',
        workspace: '/tmp/one',
        started_at: '2026-03-05T10:00:00.000Z',
        phase: 4,
        tokens_in: 1500,
        tokens_out: 500,
      }),
      createEntry({
        issue_id: 'ISSUE-200',
        attempt: 1,
        pid: 5678,
        session_id: 'zyxwvu9876543210',
        workspace: '/tmp/two',
        started_at: '2026-03-05T10:01:00.000Z',
        phase: 6,
        tokens_in: 250,
        tokens_out: 125,
      }),
    ]

    render(<SessionsTable entries={entries} />)

    const table = screen.getByRole('table', { name: '运行会话' })
    const rows = within(table).getAllByRole('row')

    expect(rows.length).toBe(3)
    expectInDocument(screen.getByText('ISSUE-100'))
    expectInDocument(screen.getByText('ISSUE-200'))
    expectInDocument(screen.getByText('AgentRunning'))
    expectInDocument(screen.getByText('Done'))
  })

  it('renders empty state when there are no entries', () => {
    render(<SessionsTable entries={[]} />)

    expectInDocument(screen.getByText('暂无运行会话'))
  })

  it('prefers server phase labels', () => {
    render(<SessionsTable entries={[createEntry({ phase_label: 'AgentRunning · turn 3' })]} />)

    expectInDocument(screen.getByText('AgentRunning · turn 3'))
  })

  it('falls back to client phase labels', () => {
    render(<SessionsTable entries={[createEntry({ phase: 4, phase_label: '' })]} />)

    expectInDocument(screen.getByText('AgentRunning'))
    expect(screen.queryByText('4')).toBeNull()
  })

  it('renders diff stats and iteration badge', () => {
    render(
      <SessionsTable
        entries={[
          createEntry({
            diff_added: 47,
            diff_removed: 3,
            diff_files: 2,
            diff_status: 'ok',
            iteration: 3,
            iteration_max: 50,
          }),
        ]}
      />,
    )

    expectInDocument(screen.getByText('+47 -3 (2 files)'))
    expectInDocument(screen.getByText('iter 3/50'))
  })

  it('renders diff timeout as unknown with tooltip', () => {
    render(<SessionsTable entries={[createEntry({ diff_status: 'timeout' })]} />)

    const timeoutCell = screen.getByText('?')
    expectInDocument(timeoutCell)
    expect(timeoutCell.getAttribute('title')).toBe('timeout')
  })

  // T6: five-step stage pill tests
  it('renders stage pill with first box highlighted for stage 1', () => {
    render(<SessionsTable entries={[createEntry({ agent_stage: 'Exploration', agent_stage_step: 1 })]} />)

    const pill = screen.getByLabelText('Exploration')
    expectInDocument(pill)
    // Stage name label above boxes
    expectInDocument(screen.getByText('探索'))
  })

  it('renders stage pill with last box highlighted for stage 5', () => {
    render(<SessionsTable entries={[createEntry({ agent_stage: 'Wrapping', agent_stage_step: 5 })]} />)

    const pill = screen.getByLabelText('Wrapping')
    expectInDocument(pill)
    expectInDocument(screen.getByText('收尾'))
  })

  it('falls back to phase_label when agent_stage is empty', () => {
    render(<SessionsTable entries={[createEntry({ agent_stage: '', agent_stage_step: 0, phase_label: 'AgentRunning' })]} />)

    expectInDocument(screen.getByText('AgentRunning'))
    expect(screen.queryByLabelText('Exploration')).toBeNull()
  })

  it('falls back to phase_label when agent_stage fields are absent', () => {
    render(<SessionsTable entries={[createEntry({ phase: 4, phase_label: 'AgentRunning' })]} />)

    expectInDocument(screen.getByText('AgentRunning'))
  })

  // T7: "Done by" column tests
  it('renders ~HH:MM for medium confidence ETA', () => {
    const futureTime = new Date(Date.now() + 15 * 60 * 1000).toISOString()
    render(
      <SessionsTable
        entries={[createEntry({ eta_completion_at: futureTime, eta_confidence: 'medium' })]}
      />,
    )

    const cells = screen.getAllByRole('cell')
    const doneByCell = cells.find((c) => /^~\d{2}:\d{2}$/.test(c.textContent ?? ''))
    expect(doneByCell).toBeDefined()
    expectInDocument(doneByCell)
  })

  it('renders ~HH:MM for high confidence ETA', () => {
    const futureTime = new Date(Date.now() + 20 * 60 * 1000).toISOString()
    render(
      <SessionsTable
        entries={[createEntry({ eta_completion_at: futureTime, eta_confidence: 'high' })]}
      />,
    )

    const cells = screen.getAllByRole('cell')
    const doneByCell = cells.find((c) => /^~\d{2}:\d{2}$/.test(c.textContent ?? ''))
    expect(doneByCell).toBeDefined()
    expectInDocument(doneByCell)
  })

  it('renders elapsed fallback for low confidence after 60s', () => {
    const startedAt = new Date(Date.now() - 5 * 60 * 1000).toISOString()
    render(
      <SessionsTable
        entries={[createEntry({ started_at: startedAt, eta_confidence: 'low', eta_completion_at: '' })]}
      />,
    )

    expectInDocument(screen.getByText(/已运行 \d+m，正常/))
  })

  it('renders dash for entries under 60s elapsed', () => {
    const startedAt = new Date(Date.now() - 30 * 1000).toISOString()
    render(
      <SessionsTable
        entries={[createEntry({ started_at: startedAt, eta_confidence: 'low', eta_completion_at: '' })]}
      />,
    )

    const cells = screen.getAllByRole('cell')
    const dashCell = cells.find((c) => c.textContent === '—')
    expect(dashCell).toBeDefined()
  })

  it('does not render any countdown text in any ETA state', () => {
    const futureTime = new Date(Date.now() + 15 * 60 * 1000).toISOString()
    const startedAt = new Date(Date.now() - 5 * 60 * 1000).toISOString()

    const { container: c1 } = render(
      <SessionsTable entries={[createEntry({ eta_completion_at: futureTime, eta_confidence: 'medium', started_at: startedAt })]} />,
    )
    expect(c1.textContent).not.toMatch(/\d+\s?(min|m|sec|s) remaining/i)
    cleanup()

    const { container: c2 } = render(
      <SessionsTable entries={[createEntry({ eta_confidence: 'low', eta_completion_at: '', started_at: startedAt })]} />,
    )
    expect(c2.textContent).not.toMatch(/\d+\s?(min|m|sec|s) remaining/i)
    cleanup()

    const { container: c3 } = render(
      <SessionsTable entries={[createEntry({ eta_confidence: '', eta_completion_at: '', started_at: new Date(Date.now() - 30 * 1000).toISOString() })]} />,
    )
    expect(c3.textContent).not.toMatch(/\d+\s?(min|m|sec|s) remaining/i)
  })
})
