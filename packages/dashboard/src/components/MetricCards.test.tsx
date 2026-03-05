import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { Stats } from '../types'
import { MetricCards } from './MetricCards'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('MetricCards', () => {
  it('renders all metric cards and values from stats', () => {
    const stats: Stats = {
      Running: 3,
      MaxAgents: 5,
      TotalTokensIn: 1600,
      TotalTokensOut: 400,
      StartTime: '2026-03-05T10:00:00.000Z',
      PollCount: 42,
    }

    render(<MetricCards stats={stats} backoffCount={2} runtimeSeconds={125} />)

    expectInDocument(screen.getByText('Running'))
    expectInDocument(screen.getByText('3/5'))
    expectInDocument(screen.getByText('Retrying'))
    expectInDocument(screen.getByText('2'))
    expectInDocument(screen.getByText('Total Tokens'))
    expectInDocument(screen.getByText('2.0K'))
    expectInDocument(screen.getByText('1.6K in / 400 out'))
    expectInDocument(screen.getByText('Runtime'))
    expectInDocument(screen.getByText('2m 5s'))
  })
})
