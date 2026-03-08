import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { BoardIssue } from '../types'
import { BoardView } from './BoardView'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

function expectValue(value: unknown, expected: string) {
  ;(expect(value) as any).toHaveValue(expected)
}

function expectClass(value: unknown, expected: string) {
  ;(expect(value) as any).toHaveClass(expected)
}

function makeIssue(partial: Partial<BoardIssue>): BoardIssue {
  return {
    id: partial.id ?? 'issue-1',
    identifier: partial.identifier ?? 'BOARD-1',
    title: partial.title ?? 'Initial issue',
    description: partial.description ?? 'Issue description',
    state: partial.state ?? 'open',
    assignee: partial.assignee,
    created_at: partial.created_at ?? '2026-03-05T10:00:00.000Z',
    updated_at: partial.updated_at ?? '2026-03-05T10:00:00.000Z',
  }
}

afterEach(() => {
  cleanup()
})

describe('BoardView', () => {
  it('renders empty issues list', () => {
    render(<BoardView issues={[]} />)

    expectInDocument(screen.getByText('No board issues'))
    expectInDocument(screen.getByLabelText('Issue title'))
    expectInDocument(screen.getByLabelText('Issue description'))
  })

  it('renders issues with correct columns', () => {
    const issues: BoardIssue[] = [
      makeIssue({ id: 'a', identifier: 'BOARD-12', title: 'Wire dashboard', state: 'open' }),
      makeIssue({ id: 'b', identifier: 'BOARD-13', title: 'Ship board view', state: 'done' }),
    ]

    render(<BoardView issues={issues} />)

    const table = screen.getByRole('table', { name: 'Board issues table' })
    expectInDocument(within(table).getByText('Identifier'))
    expectInDocument(within(table).getByText('Title'))
    expectInDocument(within(table).getByText('State'))
    expectInDocument(within(table).getByText('Assignee'))
    expectInDocument(within(table).getByText('Updated'))
    expectInDocument(within(table).getByText('BOARD-12'))
    expectInDocument(within(table).getByText('BOARD-13'))
  })

  it('create form submits correctly', async () => {
    const requests: Array<{ input: RequestInfo | URL; init?: RequestInit }> = []
    const originalFetch = globalThis.fetch

    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      requests.push({ input, init })
      return Promise.resolve(
        new Response(
          JSON.stringify(
            makeIssue({
              id: 'new',
              identifier: 'BOARD-99',
              title: 'New issue',
              description: 'From create form',
              state: 'open',
              updated_at: '2026-03-06T10:00:00.000Z',
            }),
          ),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      )
    }) as typeof fetch

    render(<BoardView issues={[]} />)

    fireEvent.change(screen.getByLabelText('Issue title'), { target: { value: 'New issue' } })
    fireEvent.change(screen.getByLabelText('Issue description'), { target: { value: 'From create form' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => {
      expect(requests).toHaveLength(1)
    })

    const first = requests[0]
    expect(first.input).toBe('/api/v1/board/issues')
    expect(first.init?.method).toBe('POST')
    expect(first.init?.headers).toEqual({ 'Content-Type': 'application/json' })
    expect(first.init?.body).toBe(JSON.stringify({ title: 'New issue', description: 'From create form' }))

    await waitFor(() => {
      expectValue(screen.getByLabelText('Issue title'), '')
      expectValue(screen.getByLabelText('Issue description'), '')
    })

    globalThis.fetch = originalFetch
  })

  it('state badges show correct colors', () => {
    const issues: BoardIssue[] = [
      makeIssue({ id: 'a', identifier: 'BOARD-1', state: 'open' }),
      makeIssue({ id: 'b', identifier: 'BOARD-2', state: 'in_progress' }),
      makeIssue({ id: 'c', identifier: 'BOARD-3', state: 'done' }),
    ]

    render(<BoardView issues={issues} />)

    expectInDocument(screen.getByText('Open'))
    expectInDocument(screen.getByText('In Progress'))
    expectInDocument(screen.getByText('Done'))

    expectClass(screen.getByText('Open'), 'board-view__state-badge--open')
    expectClass(screen.getByText('In Progress'), 'board-view__state-badge--in-progress')
    expectClass(screen.getByText('Done'), 'board-view__state-badge--done')
  })

  it('sorts issues by updated_at descending', () => {
    const issues: BoardIssue[] = [
      makeIssue({ id: 'older', identifier: 'BOARD-older', updated_at: '2026-03-05T10:00:00.000Z' }),
      makeIssue({ id: 'newer', identifier: 'BOARD-newer', updated_at: '2026-03-05T12:00:00.000Z' }),
    ]

    render(<BoardView issues={issues} />)

    const rows = within(screen.getByRole('table', { name: 'Board issues table' })).getAllByRole('row')
    expect(rows).toHaveLength(3)

    const firstDataRow = rows[1]
    expectInDocument(within(firstDataRow).getByText('BOARD-newer'))
  })
})
