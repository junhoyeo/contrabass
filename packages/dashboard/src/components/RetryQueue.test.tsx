import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, render, screen } from '@testing-library/react'
import '@testing-library/jest-dom'
import type { BackoffEntry } from '../types'
import { RetryQueue } from './RetryQueue'

function expectInDocument(value: unknown) {
  ;(expect(value) as any).toBeInTheDocument()
}

afterEach(() => {
  cleanup()
})

describe('RetryQueue', () => {
  it('renders retry rows and error messages', () => {
    const longError = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890'
    const entries: BackoffEntry[] = [
      {
        issue_id: 'ISSUE-10',
        attempt: 1,
        retry_at: 'not-a-date',
        error: 'failed to acquire lock',
      },
      {
        issue_id: 'ISSUE-20',
        attempt: 2,
        retry_at: '2000-01-01T00:00:00.000Z',
        error: 'process timed out',
      },
      {
        issue_id: 'ISSUE-30',
        attempt: 3,
        retry_at: '2000-01-01T00:00:00.000Z',
        error: longError,
      },
    ]

    render(<RetryQueue entries={entries} />)

    expectInDocument(screen.getByRole('table'))
    expectInDocument(screen.getByText('ISSUE-10'))
    expectInDocument(screen.getByText('ISSUE-20'))
    expectInDocument(screen.getByText('failed to acquire lock'))
    expectInDocument(screen.getByText('process timed out'))
    expectInDocument(screen.getByText(`${longError.slice(0, 57)}...`))
    expectInDocument(screen.getByText('Unknown'))
    expect(screen.getAllByText('Ready')).toHaveLength(2)
  })

  it('renders empty state when queue is empty', () => {
    render(<RetryQueue entries={[]} />)

    expectInDocument(screen.getByText('No retries pending'))
  })
})
