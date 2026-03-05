import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen, within } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { RunningEntry } from '../types'
import { SessionsTable } from './SessionsTable'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('SessionsTable', () => {
  it('renders rows for each running entry with issue IDs', () => {
    const entries: RunningEntry[] = [
      {
        issue_id: 'ISSUE-100',
        attempt: 2,
        pid: 1234,
        session_id: 'abcdef1234567890',
        workspace: '/tmp/one',
        started_at: '2026-03-05T10:00:00.000Z',
        phase: 4,
        tokens_in: 1500,
        tokens_out: 500,
      },
      {
        issue_id: 'ISSUE-200',
        attempt: 1,
        pid: 5678,
        session_id: 'zyxwvu9876543210',
        workspace: '/tmp/two',
        started_at: '2026-03-05T10:01:00.000Z',
        phase: 6,
        tokens_in: 250,
        tokens_out: 125,
      },
    ]

    render(<SessionsTable entries={entries} />)

    const table = screen.getByRole('table', { name: 'Running sessions' })
    const rows = within(table).getAllByRole('row')

    expect(rows.length).toBe(3)
    expectInDocument(screen.getByText('ISSUE-100'))
    expectInDocument(screen.getByText('ISSUE-200'))
    expectInDocument(screen.getByText('StreamingTurn'))
    expectInDocument(screen.getByText('Succeeded'))
  })

  it('renders empty state when there are no entries', () => {
    render(<SessionsTable entries={[]} />)

    expectInDocument(screen.getByText('No running sessions'))
  })
})
