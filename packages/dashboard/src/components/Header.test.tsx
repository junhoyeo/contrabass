import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import { Header } from './Header'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('Header', () => {
  it('renders title, live badge, and runtime', () => {
    render(<Header connected runtimeSeconds={135} />)

    expectInDocument(screen.getByRole('heading', { name: 'Ziikoo' }))
    expectInDocument(screen.getByText('在线'))
    expectInDocument(screen.getByText('运行时长'))
    expectInDocument(screen.getByText('2分 15秒'))
  })

  it('renders offline badge when disconnected', () => {
    render(<Header connected={false} runtimeSeconds={1} />)

    expectInDocument(screen.getByText('离线'))
    expectInDocument(screen.getByText('运行时长'))
    expectInDocument(screen.getByText('0分 1秒'))
  })
})
