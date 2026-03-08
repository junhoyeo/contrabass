import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type {
  AgentLogEvent,
  BoardIssue,
  OrchestratorEvent,
  StateSnapshot,
  TeamSnapshot,
  WebEvent,
} from '../types'
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

function makeWebEvent(kind: WebEvent['kind'], type: string, payload: unknown): WebEvent {
  return {
    kind,
    type,
    payload,
    timestamp: '2026-03-05T11:00:00.000Z',
  }
}

function makeTeamSnapshot(): TeamSnapshot {
  return {
    name: 'alpha',
    phase: {
      phase: 'team-plan',
      fix_loop_count: 0,
      transitions: [],
      artifacts: {},
    },
    workers: [],
    tasks: [],
    config: {
      max_workers: 3,
      max_fix_loops: 2,
      claim_lease_seconds: 300,
      state_dir: '/tmp/team',
      agent_type: 'executor',
    },
    created_at: '2026-03-05T10:00:00.000Z',
  }
}

describe('useSSE state helpers', () => {
  it('starts disconnected with null state', () => {
    expect(INITIAL_STATE).toEqual({
      state: null,
      connected: false,
      error: null,
      teamSnapshot: null,
      boardIssues: [],
      agentLogs: [],
    })
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

  it('parses orchestrator web events from EventSource listeners', async () => {
    const originalEventSource = globalThis.EventSource
    MockEventSource.instances = []
    globalThis.EventSource = MockEventSource as unknown as typeof EventSource

    try {
      const { result } = renderHook(() => useSSE())
      const eventSource = MockEventSource.instances[0]
      const snapshot = makeSnapshot()
      const orchestratorEvent: OrchestratorEvent = {
        Type: 0,
        IssueID: 'ISSUE-1',
        Data: {
          Stats: {
            ...snapshot.stats,
            Running: 4,
          },
        },
        Timestamp: '2026-03-05T10:05:00.000Z',
      }

      eventSource.onopen?.(new Event('open'))
      eventSource.emit('snapshot', snapshot)
      eventSource.emit('StatusUpdate', makeWebEvent('orchestrator', 'StatusUpdate', orchestratorEvent))

      await waitFor(() => {
        expect(result.current.state?.stats.Running).toBe(4)
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

  it('handles orchestrator web_event action through existing applyEvent logic', () => {
    const snapshot = makeSnapshot()
    const state = sseReducer(INITIAL_STATE, { type: 'snapshot', data: snapshot })
    const orchestratorEvent: OrchestratorEvent = {
      Type: 1,
      IssueID: 'ISSUE-9',
      Data: {
        Attempt: 1,
        PID: 9999,
        SessionID: 'session-issue-9',
        Workspace: '/tmp/issue-9',
      },
      Timestamp: '2026-03-05T10:10:00.000Z',
    }

    const next = sseReducer(state, {
      type: 'web_event',
      data: makeWebEvent('orchestrator', 'AgentStarted', orchestratorEvent),
    })

    expect(next.state?.running.find((entry) => entry.issue_id === 'ISSUE-9')).toBeTruthy()
  })

  it('updates teamSnapshot for team events', () => {
    const created = sseReducer(INITIAL_STATE, {
      type: 'web_event',
      data: makeWebEvent('team', 'team_created', {
        type: 'team_created',
        team_name: 'team-1',
        data: {
          config: {
            max_workers: 5,
          },
        },
        timestamp: '2026-03-05T10:11:00.000Z',
      }),
    })

    expect(created.teamSnapshot?.name).toBe('team-1')
    expect(created.teamSnapshot?.config.max_workers).toBe(5)

    const phaseStarted = sseReducer(created, {
      type: 'web_event',
      data: makeWebEvent('team', 'phase_started', {
        type: 'phase_started',
        team_name: 'team-1',
        data: {
          phase: 'team-exec',
          fix_loop_count: 1,
          artifacts: {
            plan: '/tmp/plan.md',
          },
        },
        timestamp: '2026-03-05T10:12:00.000Z',
      }),
    })

    expect(phaseStarted.teamSnapshot?.phase.phase).toBe('team-exec')
    expect(phaseStarted.teamSnapshot?.phase.fix_loop_count).toBe(1)
    expect(phaseStarted.teamSnapshot?.phase.artifacts.plan).toBe('/tmp/plan.md')

    const workerStarted = sseReducer(phaseStarted, {
      type: 'web_event',
      data: makeWebEvent('team', 'worker_started', {
        type: 'worker_started',
        team_name: 'team-1',
        data: {
          id: 'worker-1',
          agent_type: 'executor',
          status: 'running',
          work_dir: '/tmp/worker-1',
          started_at: '2026-03-05T10:13:00.000Z',
          last_heartbeat: '2026-03-05T10:13:05.000Z',
        },
        timestamp: '2026-03-05T10:13:00.000Z',
      }),
    })

    expect(workerStarted.teamSnapshot?.workers).toHaveLength(1)
    expect(workerStarted.teamSnapshot?.workers[0]?.id).toBe('worker-1')
  })

  it('updates boardIssues for created, updated, and moved events', () => {
    const issueCreated: BoardIssue = {
      id: '1',
      identifier: 'B-1',
      title: 'Initial title',
      description: 'desc',
      state: 'todo',
      created_at: '2026-03-05T10:14:00.000Z',
      updated_at: '2026-03-05T10:14:00.000Z',
    }

    const created = sseReducer(INITIAL_STATE, {
      type: 'web_event',
      data: makeWebEvent('board', 'board_issue_created', {
        action: 'created',
        issue: issueCreated,
      }),
    })

    expect(created.boardIssues).toHaveLength(1)
    expect(created.boardIssues[0]?.title).toBe('Initial title')

    const updated = sseReducer(created, {
      type: 'web_event',
      data: makeWebEvent('board', 'board_issue_updated', {
        action: 'updated',
        issue: {
          ...issueCreated,
          title: 'Updated title',
          updated_at: '2026-03-05T10:15:00.000Z',
        },
      }),
    })

    expect(updated.boardIssues).toHaveLength(1)
    expect(updated.boardIssues[0]?.title).toBe('Updated title')

    const moved = sseReducer(updated, {
      type: 'web_event',
      data: makeWebEvent('board', 'board_issue_moved', {
        action: 'moved',
        issue: {
          ...issueCreated,
          state: 'in_progress',
          updated_at: '2026-03-05T10:16:00.000Z',
        },
      }),
    })

    expect(moved.boardIssues[0]?.state).toBe('in_progress')
  })

  it('appends agent logs and caps list to last 1000 entries', () => {
    let current = INITIAL_STATE

    for (let index = 1; index <= 1005; index += 1) {
      const logEvent: AgentLogEvent = {
        worker_id: `worker-${index % 3}`,
        line: `log-${index}`,
        stream: 'stdout',
        timestamp: `2026-03-05T10:17:${String(index % 60).padStart(2, '0')}.000Z`,
      }

      current = sseReducer(current, {
        type: 'web_event',
        data: makeWebEvent('agent_log', 'agent_log', logEvent),
      })
    }

    expect(current.agentLogs).toHaveLength(1000)
    expect(current.agentLogs[0]?.line).toBe('log-6')
    expect(current.agentLogs[999]?.line).toBe('log-1005')
  })

  it('ignores unknown web event kinds', () => {
    const seededState = {
      ...INITIAL_STATE,
      teamSnapshot: makeTeamSnapshot(),
    }
    const next = sseReducer(seededState, {
      type: 'web_event',
      data: {
        kind: 'unexpected' as unknown as WebEvent['kind'],
        type: 'mystery_event',
        payload: { foo: 'bar' },
        timestamp: '2026-03-05T10:18:00.000Z',
      },
    })

    expect(next).toEqual(seededState)
  })
})

beforeEach(() => {
  MockEventSource.instances = []
})

afterEach(() => {
  cleanup()
})
