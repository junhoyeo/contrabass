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

    expectInDocument(screen.getByRole('heading', { name: 'Contrabass' }))
    expectInDocument(screen.getByText('Live'))
    expectInDocument(screen.getByText('Runtime'))
    expectInDocument(screen.getByText('2m 15s'))
  })

  it('renders offline badge when disconnected', () => {
    render(<Header connected={false} runtimeSeconds={1} />)

    expectInDocument(screen.getByText('Offline'))
    expectInDocument(screen.getByText('Runtime'))
    expectInDocument(screen.getByText('0m 1s'))
  })
})
