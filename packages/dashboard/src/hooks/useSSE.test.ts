import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type { OrchestratorEvent, StateSnapshot } from '../types'
import { INITIAL_STATE, applyEvent, sseReducer, useSSE } from './useSSE'

class MockEventSource {
  static instances: MockEventSource[] = []

  readonly listeners = new Map<string, Array<(event: MessageEvent) => void>>()
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null

  constructor(readonly url: string) {
    MockEventSource.instances.push(this)
  }

  addEventListener(name: string, callback: (event: MessageEvent) => void) {
    const callbacks = this.listeners.get(name) ?? []
    callbacks.push(callback)
    this.listeners.set(name, callbacks)
  }

  close() {}

  emit(name: string, payload: unknown) {
    const callbacks = this.listeners.get(name) ?? []
    const event = { data: JSON.stringify(payload) } as MessageEvent

    for (const callback of callbacks) {
      callback(event)
    }
  }
}

function makeSnapshot(): StateSnapshot {
  return {
    stats: {
      Running: 1,
      MaxAgents: 4,
      TotalTokensIn: 100,
      TotalTokensOut: 50,
      StartTime: '2026-03-05T10:00:00.000Z',
      PollCount: 10,
    },
    running: [
      {
        issue_id: 'ISSUE-1',
        attempt: 1,
        pid: 2000,
        session_id: 'session-000001',
        workspace: '/tmp/ws',
        started_at: '2026-03-05T10:00:00.000Z',
        phase: 4,
        tokens_in: 100,
        tokens_out: 50,
      },
    ],
    backoff: [
      {
        issue_id: 'ISSUE-2',
        attempt: 2,
        retry_at: '2026-03-05T10:10:00.000Z',
        error: 'rate limited',
      },
    ],
    issues: {},
    generated_at: '2026-03-05T10:00:01.000Z',
  }
}

describe('useSSE state helpers', () => {
  it('starts disconnected with null state', () => {
    expect(INITIAL_STATE).toEqual({ state: null, connected: false, error: null })
  })

  it('parses snapshot event from EventSource listeners', async () => {
    const originalEventSource = globalThis.EventSource
    MockEventSource.instances = []
    globalThis.EventSource = MockEventSource as unknown as typeof EventSource

    try {
      const { result } = renderHook(() => useSSE())
      const eventSource = MockEventSource.instances[0]
      const snapshot = makeSnapshot()

      expect(result.current.state).toBeNull()
      expect(result.current.connected).toBe(false)
      expect(eventSource.url).toBe('/api/v1/events')

      eventSource.onopen?.(new Event('open'))
      eventSource.emit('snapshot', snapshot)

      await waitFor(() => {
        expect(result.current.connected).toBe(true)
        expect(result.current.state?.stats.Running).toBe(1)
      })
    } finally {
      globalThis.EventSource = originalEventSource
      cleanup()
    }
  })

  it('handles all orchestrator event types in applyEvent', () => {
    const snapshot = makeSnapshot()

    const statusUpdate: OrchestratorEvent = {
      Type: 0,
      IssueID: 'ISSUE-1',
      Data: {
        Stats: {
          ...snapshot.stats,
          Running: 7,
        },
      },
      Timestamp: '2026-03-05T10:05:00.000Z',
    }

    const agentStarted: OrchestratorEvent = {
      Type: 1,
      IssueID: 'ISSUE-3',
      Data: {
        Attempt: 1,
        PID: 3333,
        SessionID: 'session-issue-3',
        Workspace: '/tmp/issue-3',
      },
      Timestamp: '2026-03-05T10:06:00.000Z',
    }

    const agentFinished: OrchestratorEvent = {
      Type: 2,
      IssueID: 'ISSUE-1',
      Data: {
        Attempt: 1,
        Phase: 6,
        TokensIn: 20,
        TokensOut: 30,
      },
      Timestamp: '2026-03-05T10:07:00.000Z',
    }

    const backoffEnqueued: OrchestratorEvent = {
      Type: 3,
      IssueID: 'ISSUE-4',
      Data: {
        Attempt: 2,
        RetryAt: '2026-03-05T10:15:00.000Z',
        Error: 'overloaded',
      },
      Timestamp: '2026-03-05T10:08:00.000Z',
    }

    const issueReleased: OrchestratorEvent = {
      Type: 4,
      IssueID: 'ISSUE-2',
      Data: {
        Attempt: 2,
      },
      Timestamp: '2026-03-05T10:09:00.000Z',
    }

    const afterStatus = applyEvent(snapshot, statusUpdate)
    expect(afterStatus.stats.Running).toBe(7)

    const afterStart = applyEvent(afterStatus, agentStarted)
    expect(afterStart.running.find((entry) => entry.issue_id === 'ISSUE-3')).toBeTruthy()
    expect(afterStart.stats.Running).toBe(afterStart.running.length)

    const afterFinish = applyEvent(afterStart, agentFinished)
    expect(afterFinish.running.find((entry) => entry.issue_id === 'ISSUE-1')).toBeUndefined()
    expect(afterFinish.stats.TotalTokensIn).toBe(afterStart.stats.TotalTokensIn)
    expect(afterFinish.stats.TotalTokensOut).toBe(afterStart.stats.TotalTokensOut)

    const afterBackoff = applyEvent(afterFinish, backoffEnqueued)
    expect(afterBackoff.backoff.find((entry) => entry.issue_id === 'ISSUE-4')).toBeTruthy()

    const afterRelease = applyEvent(afterBackoff, issueReleased)
    expect(
      afterRelease.backoff.find((entry) => entry.issue_id === 'ISSUE-2' && entry.attempt === 2),
    ).toBeUndefined()
  })

  it('reduces snapshot and connection actions', () => {
    const snapshot = makeSnapshot()

    const afterSnapshot = sseReducer(INITIAL_STATE, { type: 'snapshot', data: snapshot })
    expect(afterSnapshot.state).toEqual(snapshot)
    expect(afterSnapshot.connected).toBe(true)
    expect(afterSnapshot.error).toBeNull()

    const afterDisconnected = sseReducer(afterSnapshot, { type: 'disconnected' })
    expect(afterDisconnected.connected).toBe(false)

    const afterError = sseReducer(afterDisconnected, { type: 'error', message: 'network failure' })
    expect(afterError.connected).toBe(false)
    expect(afterError.error).toBe('network failure')
  })
})

beforeEach(() => {
  MockEventSource.instances = []
})

afterEach(() => {
  cleanup()
})
