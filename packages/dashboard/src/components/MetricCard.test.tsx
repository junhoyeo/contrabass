import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import { MetricCard } from './MetricCard'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('MetricCard', () => {
  it('renders title and value', () => {
    render(<MetricCard title="Running" value="3/5" />)

    expectInDocument(screen.getByText('Running'))
    expectInDocument(screen.getByText('3/5'))
  })

  it('renders numeric values and subtitle', () => {
    render(<MetricCard title="Retrying" value={12} subtitle="Backoff queue" />)

    expectInDocument(screen.getByText('12'))
    expectInDocument(screen.getByText('Backoff queue'))
  })
})
