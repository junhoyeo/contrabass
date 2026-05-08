import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import { RateLimits } from './RateLimits'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('RateLimits', () => {
  it('renders localized empty state', () => {
    render(<RateLimits limits={[]} />)

    expectInDocument(screen.getByText('暂无活跃速率限制'))
  })

  it('renders localized labels for rate limits', () => {
    render(<RateLimits limits={[{ name: 'requests', remaining: 42, resetAt: '2026-03-05T10:00:00.000Z' }]} />)

    expectInDocument(screen.getByRole('region', { name: '速率限制' }))
    expectInDocument(screen.getByText('限制'))
    expectInDocument(screen.getByText('剩余'))
    expectInDocument(screen.getByText('重置时间'))
    expectInDocument(screen.getByText('requests'))
    expectInDocument(screen.getByText('42'))
  })
})
