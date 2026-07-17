import { afterEach, describe, expect, it } from 'bun:test'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import type {
  AgentLogEvent,
  BoardIssue,
  OrchestratorEvent,
  StateSnapshot,
  TeamSnapshot,
  WebEvent,
} from '../types'
import { zhCN } from '../i18n/messages'
import { INITIAL_STATE, applyEvent, sseReducer, useSSE } from './useSSE'

class MockStreamableHTTP {
  private controller: ReadableStreamDefaultController<Uint8Array> | null = null
  readonly stream: ReadableStream<Uint8Array>

  constructor() {
    this.stream = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller
      },
    })
  }

  enqueueMessage(message: unknown) {
    this.controller?.enqueue(
      new TextEncoder().encode(`event: message\ndata: ${JSON.stringify(message)}\n\n`),
    )
  }

  enqueueRaw(data: string) {
    this.controller?.enqueue(new TextEncoder().encode(data))
  }

  close() {
    try {
      this.controller?.close()
    } catch {
      // Already closed.
    }
    this.controller = null
  }
}

interface FetchCall {
  url: string
  init?: RequestInit
}

function installStreamableFetch(stream: MockStreamableHTTP): {
  calls: FetchCall[]
  restore: () => void
}
function installStreamableFetch(streams: MockStreamableHTTP[]): {
  calls: FetchCall[]
  restore: () => void
}
function installStreamableFetch(streamOrStreams: MockStreamableHTTP | MockStreamableHTTP[]): {
  calls: FetchCall[]
  restore: () => void
} {
  const originalFetch = globalThis.fetch
  const calls: FetchCall[] = []
  const streams = Array.isArray(streamOrStreams) ? streamOrStreams : [streamOrStreams]
  let streamIndex = 0

  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    calls.push({ url, init })

    if (url === '/api/v1/stream') {
      const nextStream = streams[Math.min(streamIndex, streams.length - 1)]
      streamIndex += 1
      return Promise.resolve(
        new Response(nextStream.stream, {
          status: 200,
          headers: { 'Content-Type': 'text/event-stream' },
        }),
      )
    }

    if (url === '/api/v1/board/issues') {
      return Promise.resolve(new Response('', { status: 404 }))
    }

    if (url === '/api/v1/state') {
      return Promise.resolve(Response.json(makeSnapshot()))
    }

    return Promise.reject(new Error(`unexpected fetch ${url}`))
  }) as typeof fetch

  return {
    calls,
    restore: () => {
      globalThis.fetch = originalFetch
    },
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

function makeWebEvent(
  kind: WebEvent['kind'],
  type: string,
  payload: unknown,
  timestamp = '2026-03-05T11:00:00.000Z',
): WebEvent {
  return {
    kind,
    type,
    payload,
    timestamp,
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
      boardAvailable: false,
      boardIssues: [],
      agentLogs: [],
      queueEvents: [],
      streamWatermark: null,
    })
  })

  it('parses snapshot messages from the Streamable HTTP channel', async () => {
    const stream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch(stream)

    try {
      const { result } = renderHook(() => useSSE())
      const snapshot = makeSnapshot()

      expect(result.current.state).toBeNull()
      expect(result.current.connected).toBe(false)

      await waitFor(() => {
        expect(mockFetch.calls[0]?.url).toBe('/api/v1/stream')
      })
      expect(mockFetch.calls[0]?.init?.method).toBe('POST')
      expect(mockFetch.calls[0]?.init?.headers).toMatchObject({
        Accept: 'application/json, text/event-stream',
      })

      stream.enqueueMessage({ jsonrpc: '2.0', method: 'dashboard.snapshot', params: snapshot })

      await waitFor(() => {
        expect(result.current.connected).toBe(true)
        expect(result.current.state?.stats.Running).toBe(1)
      })
    } finally {
      stream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('normalizes nullable collection fields in snapshot messages', async () => {
    const stream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch(stream)

    try {
      const { result } = renderHook(() => useSSE())
      const snapshot = {
        ...makeSnapshot(),
        running: null,
        backoff: null,
        issues: null,
      }

      await waitFor(() => {
        expect(mockFetch.calls[0]?.url).toBe('/api/v1/stream')
      })
      stream.enqueueMessage({ jsonrpc: '2.0', method: 'dashboard.snapshot', params: snapshot })

      await waitFor(() => {
        expect(result.current.state?.running).toEqual([])
        expect(result.current.state?.backoff).toEqual([])
        expect(result.current.state?.issues).toEqual({})
      })
    } finally {
      stream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('parses orchestrator web events from the Streamable HTTP channel', async () => {
    const stream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch(stream)

    try {
      const { result } = renderHook(() => useSSE())
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

      await waitFor(() => {
        expect(mockFetch.calls[0]?.url).toBe('/api/v1/stream')
      })
      stream.enqueueMessage({ jsonrpc: '2.0', method: 'dashboard.snapshot', params: snapshot })
      stream.enqueueMessage({
        jsonrpc: '2.0',
        method: 'dashboard.event',
        params: makeWebEvent('orchestrator', 'StatusUpdate', orchestratorEvent),
      })

      await waitFor(() => {
        expect(result.current.state?.stats.Running).toBe(4)
      })
    } finally {
      stream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('rejects malformed snapshot messages from the Streamable HTTP channel', async () => {
    const stream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch(stream)

    try {
      const { result } = renderHook(() => useSSE())

      await waitFor(() => {
        expect(mockFetch.calls[0]?.url).toBe('/api/v1/stream')
      })
      stream.enqueueMessage({
        jsonrpc: '2.0',
        method: 'dashboard.snapshot',
        params: {
          generated_at: '2026-03-05T10:00:01.000Z',
          stats: { Running: 1 },
          running: [],
          backoff: [],
          issues: null,
        },
      })

      await waitFor(() => {
        expect(result.current.error).toBe(zhCN.errors.parseSnapshot)
        expect(result.current.state).toBeNull()
      })
    } finally {
      stream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('rejects unsupported Streamable HTTP response content types', async () => {
    const originalFetch = globalThis.fetch
    const calls: FetchCall[] = []
    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      calls.push({ url, init })

      if (url === '/api/v1/stream') {
        return Promise.resolve(
          new Response('no stream here', {
            status: 200,
            headers: { 'Content-Type': 'text/plain' },
          }),
        )
      }
      if (url === '/api/v1/board/issues') {
        return Promise.resolve(new Response('', { status: 404 }))
      }
      if (url === '/api/v1/state') {
        return Promise.resolve(Response.json(makeSnapshot()))
      }
      return Promise.reject(new Error(`unexpected fetch ${url}`))
    }) as typeof fetch

    try {
      const { result } = renderHook(() => useSSE())

      await waitFor(() => {
        expect(calls[0]?.url).toBe('/api/v1/stream')
        expect(result.current.error).toBe('unsupported streamable HTTP response content type')
      })
    } finally {
      globalThis.fetch = originalFetch
      cleanup()
    }
  })

  it('caps unterminated Streamable HTTP buffers', async () => {
    const stream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch(stream)

    try {
      const { result } = renderHook(() => useSSE())

      await waitFor(() => {
        expect(mockFetch.calls[0]?.url).toBe('/api/v1/stream')
      })
      stream.enqueueRaw(`data: ${'x'.repeat(1024 * 1024 + 1)}`)

      await waitFor(() => {
        expect(result.current.error).toBe('stream buffer is too large')
      })
    } finally {
      stream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('reconnects when the Streamable HTTP channel closes', async () => {
    const firstStream = new MockStreamableHTTP()
    const secondStream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch([firstStream, secondStream])

    try {
      const { result } = renderHook(() => useSSE())

      await waitFor(() => {
        expect(mockFetch.calls.filter((call) => call.url === '/api/v1/stream')).toHaveLength(1)
      })
      firstStream.close()

      await waitFor(
        () => {
          expect(mockFetch.calls.filter((call) => call.url === '/api/v1/stream')).toHaveLength(2)
        },
        { timeout: 1500 },
      )

      secondStream.enqueueMessage({ jsonrpc: '2.0', method: 'dashboard.snapshot', params: makeSnapshot() })
      await waitFor(() => {
        expect(result.current.connected).toBe(true)
        expect(result.current.state?.stats.Running).toBe(1)
      })
    } finally {
      firstStream.close()
      secondStream.close()
      mockFetch.restore()
      cleanup()
    }
  })

  it('re-syncs board issues after the stream reconnects', async () => {
    const firstStream = new MockStreamableHTTP()
    const secondStream = new MockStreamableHTTP()
    const mockFetch = installStreamableFetch([firstStream, secondStream])

    try {
      renderHook(() => useSSE())

      await waitFor(() => {
        expect(mockFetch.calls.filter((call) => call.url === '/api/v1/board/issues')).toHaveLength(1)
      })
      firstStream.close()

      // Board events published while disconnected are lost (no hub replay),
      // so every successful reconnect must refetch the board snapshot.
      await waitFor(
        () => {
          expect(mockFetch.calls.filter((call) => call.url === '/api/v1/board/issues')).toHaveLength(2)
        },
        { timeout: 1500 },
      )
    } finally {
      firstStream.close()
      secondStream.close()
      mockFetch.restore()
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

    const refreshSnapshot = {
      ...snapshot,
      generated_at: '2026-03-05T10:00:02.000Z',
    }
    const afterRefresh = sseReducer(afterError, { type: 'snapshot', data: refreshSnapshot, source: 'refresh' })
    expect(afterRefresh.state).toEqual(refreshSnapshot)
    expect(afterRefresh.connected).toBe(false)
    expect(afterRefresh.error).toBe('network failure')
  })

  it('ignores stale snapshot refreshes', () => {
    const currentSnapshot = makeSnapshot()
    const olderSnapshot: StateSnapshot = {
      ...currentSnapshot,
      stats: {
        ...currentSnapshot.stats,
        Running: 99,
      },
      generated_at: '2026-03-05T09:59:59.000Z',
    }
    const state = sseReducer(INITIAL_STATE, { type: 'snapshot', data: currentSnapshot })

    const next = sseReducer(state, { type: 'snapshot', data: olderSnapshot })

    expect(next.state).toEqual(currentSnapshot)
    expect(next.connected).toBe(true)
    expect(next.error).toBeNull()
  })

  it('keeps newer stream event state when an older refresh snapshot arrives', () => {
    const snapshot = makeSnapshot()
    const seeded = sseReducer(INITIAL_STATE, { type: 'snapshot', data: snapshot, source: 'stream' })
    const webEvent: OrchestratorEvent = {
      Type: 0,
      IssueID: 'ISSUE-1',
      Data: {
        Stats: {
          ...snapshot.stats,
          Running: 8,
        },
      },
      Timestamp: '2026-03-05T10:05:00.000Z',
    }
    const afterEvent = sseReducer(seeded, {
      type: 'web_event',
      data: makeWebEvent('orchestrator', 'StatusUpdate', webEvent, '2026-03-05T10:05:00.000Z'),
    })
    const staleRefresh: StateSnapshot = {
      ...snapshot,
      stats: {
        ...snapshot.stats,
        Running: 99,
      },
      generated_at: '2026-03-05T10:01:00.000Z',
    }

    const next = sseReducer(afterEvent, { type: 'snapshot', data: staleRefresh, source: 'refresh' })

    expect(next.state?.stats.Running).toBe(8)
    expect(next.streamWatermark).toBe('2026-03-05T10:05:00.000Z')
  })

  it('reduces board snapshots and marks board support available', () => {
    const boardIssues: BoardIssue[] = [
      {
        id: '1',
        identifier: 'B-1',
        title: 'Snapshot issue',
        description: 'desc',
        state: 'open',
        created_at: '2026-03-05T10:14:00.000Z',
        updated_at: '2026-03-05T10:14:00.000Z',
      },
    ]

    const next = sseReducer(INITIAL_STATE, { type: 'board_snapshot', data: boardIssues })

    expect(next.boardAvailable).toBe(true)
    expect(next.boardIssues).toEqual(boardIssues)
  })

  it('keeps newer board events when an older board snapshot arrives later', () => {
    const olderIssue: BoardIssue = {
      id: '1',
      identifier: 'B-1',
      title: 'Older snapshot title',
      description: 'desc',
      state: 'open',
      created_at: '2026-03-05T10:14:00.000Z',
      updated_at: '2026-03-05T10:14:00.000Z',
    }
    const newerIssue: BoardIssue = {
      ...olderIssue,
      title: 'Newer event title',
      updated_at: '2026-03-05T10:15:00.000Z',
    }
    const seeded = {
      ...INITIAL_STATE,
      boardAvailable: true,
      boardIssues: [newerIssue],
    }

    const next = sseReducer(seeded, { type: 'board_snapshot', data: [olderIssue] })

    expect(next.boardIssues).toEqual([newerIssue])
  })

  it('removes board issues missing from an authoritative board snapshot', () => {
    const removedIssue: BoardIssue = {
      id: '1',
      identifier: 'B-1',
      title: 'Removed issue',
      description: 'desc',
      state: 'open',
      created_at: '2026-03-05T10:14:00.000Z',
      updated_at: '2026-03-05T10:15:00.000Z',
    }
    const seeded = {
      ...INITIAL_STATE,
      boardAvailable: true,
      boardIssues: [removedIssue],
    }

    const next = sseReducer(seeded, { type: 'board_snapshot', data: [] })

    expect(next.boardAvailable).toBe(true)
    expect(next.boardIssues).toEqual([])
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

  it('records queue channel events with monotonically increasing sequence ids', () => {
    const next = sseReducer(INITIAL_STATE, {
      type: 'web_event',
      data: makeWebEvent('orchestrator', 'dispatch_skipped_blocked_by', {
        issue_id: 'issue-50',
        identifier: 'ZII-50',
        blockers: 'ZII-49,ZII-48',
      }),
    })

    expect(next.queueEvents).toHaveLength(1)
    expect(next.queueEvents[0]).toMatchObject({
      issue_id: 'issue-50',
      identifier: 'ZII-50',
      blockers: 'ZII-49,ZII-48',
    })

    const after = sseReducer(next, {
      type: 'web_event',
      data: makeWebEvent('orchestrator', 'dispatch_skipped_blocked_by', {
        issue_id: 'issue-51',
        identifier: 'ZII-51',
        blockers: 'ZII-50',
      }),
    })

    // Consumers track their position by seq because the ring buffer trims
    // entries from the front once full; indices are not stable.
    expect(after.queueEvents).toHaveLength(2)
    expect(after.queueEvents[1].seq).toBeGreaterThan(after.queueEvents[0].seq)
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

  it('records tool_call team events as agent logs', () => {
    const next = sseReducer(INITIAL_STATE, {
      type: 'web_event',
      data: makeWebEvent('team', 'tool_call', {
        type: 'tool_call',
        team_name: 'team-1',
        data: {
          tool_name: 'ripgrep',
        },
        timestamp: '2026-03-05T10:13:30.000Z',
      }),
    })

    expect(next.agentLogs).toEqual([
      {
        worker_id: 'team-1',
        line: 'tool_call: ripgrep',
        stream: 'team',
        timestamp: '2026-03-05T10:13:30.000Z',
      },
    ])
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

    expect(created.boardAvailable).toBe(true)
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

afterEach(() => {
  cleanup()
})
