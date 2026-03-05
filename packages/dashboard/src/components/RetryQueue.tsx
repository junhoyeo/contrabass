import { useEffect, useState } from 'react'
import type { BackoffEntry } from '../types'
import './RetryQueue.css'

interface RetryQueueProps {
  entries: BackoffEntry[]
}

function formatRetryIn(retryAt: string, nowMs: number): { text: string; ready: boolean } {
  const retryAtMs = Date.parse(retryAt)
  if (Number.isNaN(retryAtMs)) {
    return { text: 'Unknown', ready: false }
  }

  const diffSeconds = Math.floor((retryAtMs - nowMs) / 1000)
  if (diffSeconds <= 0) {
    return { text: 'Ready', ready: true }
  }

  if (diffSeconds < 60) {
    return { text: `${diffSeconds}s`, ready: false }
  }

  const minutes = Math.floor(diffSeconds / 60)
  const seconds = diffSeconds % 60
  return { text: `${minutes}m ${seconds}s`, ready: false }
}

function truncateError(error: string, limit = 60): string {
  if (error.length <= limit) {
    return error
  }

  return `${error.slice(0, limit - 1)}...`
}

export function RetryQueue({ entries }: RetryQueueProps) {
  const [nowMs, setNowMs] = useState(() => Date.now())

  useEffect(() => {
    const timer = window.setInterval(() => {
      setNowMs(Date.now())
    }, 1000)

    return () => window.clearInterval(timer)
  }, [])

  if (entries.length === 0) {
    return (
      <section className="retry-queue retry-queue--empty" aria-live="polite">
        <p className="retry-queue__empty-text">
          <span className="retry-queue__empty-check" aria-hidden="true">
            ✓
          </span>{' '}
          No retries pending
        </p>
      </section>
    )
  }

  return (
    <section className="retry-queue" aria-label="Retry queue">
      <table className="retry-queue__table">
        <thead>
          <tr>
            <th>Issue ID</th>
            <th>Attempt</th>
            <th>Retry In</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => {
            const retryIn = formatRetryIn(entry.retry_at, nowMs)

            return (
              <tr key={`${entry.issue_id}-${entry.attempt}-${entry.retry_at}`}>
                <td className="retry-queue__mono">{entry.issue_id}</td>
                <td className="retry-queue__mono">{entry.attempt}</td>
                <td className={`retry-queue__mono ${retryIn.ready ? 'retry-queue__ready' : ''}`}>
                  {retryIn.text}
                </td>
                <td className="retry-queue__error" title={entry.error}>
                  {truncateError(entry.error)}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </section>
  )
}
