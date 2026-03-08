import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { AgentLogEvent } from '../types'
import { AgentLogs } from './AgentLogs'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

function createLog(overrides: Partial<AgentLogEvent> = {}): AgentLogEvent {
  return {
    worker_id: 'worker-a',
    line: 'hello world',
    stream: 'stdout',
    timestamp: '2026-03-07T12:34:56.000Z',
    ...overrides,
  }
}

afterEach(() => {
  cleanup()
})

describe('AgentLogs', () => {
  it('renders empty state', () => {
    render(<AgentLogs logs={[]} />)

    expectInDocument(screen.getByText('No agent logs'))
  })

  it('renders log lines with timestamp, worker, and message', () => {
    render(
      <AgentLogs
        logs={[
          createLog({ worker_id: 'worker-a', line: 'first line' }),
          createLog({ worker_id: 'worker-b', line: 'second line', timestamp: '2026-03-07T03:04:05.000Z' }),
        ]}
      />,
    )

    expectInDocument(screen.getByText('[12:34:56]'))
    expectInDocument(screen.getByText('[03:04:05]'))
    expectInDocument(screen.getByText('[worker-a]'))
    expectInDocument(screen.getByText('[worker-b]'))
    expectInDocument(screen.getByText('first line'))
    expectInDocument(screen.getByText('second line'))
  })

  it('applies stderr styling for stderr lines', () => {
    render(<AgentLogs logs={[createLog({ line: 'stderr output', stream: 'stderr' })]} />)

    const stderrLine = screen.getByText('stderr output').closest('.agent-logs__line')
    expectInDocument(stderrLine)
    expect(stderrLine?.className.includes('agent-logs__line--stderr')).toBe(true)
  })

  it('shows worker filter options and filters logs', () => {
    render(
      <AgentLogs
        logs={[
          createLog({ worker_id: 'worker-z', line: 'z-line' }),
          createLog({ worker_id: 'worker-a', line: 'a-line' }),
          createLog({ worker_id: 'worker-z', line: 'z-line-2' }),
        ]}
      />,
    )

    const filter = screen.getByLabelText('Worker') as HTMLSelectElement
    const optionValues = Array.from(filter.options).map((option) => option.value)
    expect(optionValues).toEqual(['all', 'worker-a', 'worker-z'])

    fireEvent.change(filter, { target: { value: 'worker-a' } })
    expectInDocument(screen.getByText('a-line'))
    expect(screen.queryByText('z-line')).toBeNull()
    expect(screen.queryByText('z-line-2')).toBeNull()
  })

  it('renders at most 500 log lines', () => {
    const logs = Array.from({ length: 520 }, (_, index) =>
      createLog({
        worker_id: 'worker-a',
        line: `line-${index + 1}`,
        timestamp: `2026-03-07T12:34:${String(index % 60).padStart(2, '0')}.000Z`,
      }),
    )

    const { container } = render(<AgentLogs logs={logs} />)

    expect(container.querySelectorAll('.agent-logs__line')).toHaveLength(500)
    expect(screen.queryByText('line-1')).toBeNull()
    expectInDocument(screen.getByText('line-520'))
  })
})
