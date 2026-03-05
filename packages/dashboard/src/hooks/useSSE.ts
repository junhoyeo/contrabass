import { useCallback, useEffect, useReducer, useRef } from 'react'
import type { BackoffEntry, OrchestratorEvent, RunningEntry, StateSnapshot, Stats } from '../types'

interface SSEState {
  state: StateSnapshot | null
  connected: boolean
  error: string | null
}

type SSEAction =
  | { type: 'snapshot'; data: StateSnapshot }
  | { type: 'event'; data: OrchestratorEvent }
  | { type: 'connected' }
  | { type: 'disconnected' }
  | { type: 'error'; message: string }

interface StatusUpdateData {
  Stats: Stats
}

interface AgentStartedData {
  Attempt: number
  PID: number
  SessionID: string
  Workspace: string
}

interface AgentFinishedData {
  Attempt: number
  Phase: number
  TokensIn: number
  TokensOut: number
}

interface BackoffEnqueuedData {
  Attempt: number
  RetryAt: string
  Error: string
}

interface IssueReleasedData {
  Attempt: number
}

const INITIAL_STATE: SSEState = {
  state: null,
  connected: false,
  error: null,
}

function applyEvent(snapshot: StateSnapshot, event: OrchestratorEvent): StateSnapshot {
  switch (event.Type) {
    case 0: {
      const data = event.Data as StatusUpdateData
      return {
        ...snapshot,
        stats: data.Stats,
      }
    }

    case 1: {
      const data = event.Data as AgentStartedData
      const entry: RunningEntry = {
        issue_id: event.IssueID,
        attempt: data.Attempt,
        pid: data.PID,
        session_id: data.SessionID,
        workspace: data.Workspace,
        started_at: event.Timestamp,
        phase: 0,
        tokens_in: 0,
        tokens_out: 0,
      }

      const running = [...snapshot.running.filter((item) => item.issue_id !== event.IssueID), entry]

      return {
        ...snapshot,
        running,
        stats: {
          ...snapshot.stats,
          Running: running.length,
        },
      }
    }

    case 2: {
      const data = event.Data as AgentFinishedData
      const running = snapshot.running.filter((item) => item.issue_id !== event.IssueID)

      return {
        ...snapshot,
        running,
        stats: {
          ...snapshot.stats,
          Running: running.length,
          TotalTokensIn: snapshot.stats.TotalTokensIn + data.TokensIn,
          TotalTokensOut: snapshot.stats.TotalTokensOut + data.TokensOut,
        },
      }
    }

    case 3: {
      const data = event.Data as BackoffEnqueuedData
      const entry: BackoffEntry = {
        issue_id: event.IssueID,
        attempt: data.Attempt,
        retry_at: data.RetryAt,
        error: data.Error,
      }

      const backoff = [
        ...snapshot.backoff.filter(
          (item) => !(item.issue_id === event.IssueID && item.attempt === data.Attempt),
        ),
        entry,
      ]

      return {
        ...snapshot,
        backoff,
      }
    }

    case 4: {
      const data = event.Data as IssueReleasedData
      const backoff = snapshot.backoff.filter(
        (item) => !(item.issue_id === event.IssueID && item.attempt === data.Attempt),
      )

      return {
        ...snapshot,
        backoff,
      }
    }

    default:
      return snapshot
  }
}

function sseReducer(state: SSEState, action: SSEAction): SSEState {
  switch (action.type) {
    case 'snapshot':
      return { ...state, state: action.data, connected: true, error: null }
    case 'connected':
      return { ...state, connected: true, error: null }
    case 'disconnected':
      return { ...state, connected: false }
    case 'error':
      return { ...state, error: action.message, connected: false }
    case 'event':
      if (!state.state) {
        return state
      }

      return { ...state, state: applyEvent(state.state, action.data) }
    default:
      return state
  }
}

const EVENT_NAMES = [
  'StatusUpdate',
  'AgentStarted',
  'AgentFinished',
  'BackoffEnqueued',
  'IssueReleased',
] as const

export function useSSE() {
  const [sseState, dispatch] = useReducer(sseReducer, INITIAL_STATE)
  const eventSourceRef = useRef<EventSource | null>(null)

  const connect = useCallback(() => {
    eventSourceRef.current?.close()

    const eventSource = new EventSource('/api/v1/events')
    eventSourceRef.current = eventSource

    eventSource.addEventListener('snapshot', (event) => {
      try {
        const data = JSON.parse((event as MessageEvent).data) as StateSnapshot
        dispatch({ type: 'snapshot', data })
      } catch {
        dispatch({ type: 'error', message: 'Failed to parse snapshot payload' })
      }
    })

    for (const eventName of EVENT_NAMES) {
      eventSource.addEventListener(eventName, (event) => {
        try {
          const data = JSON.parse((event as MessageEvent).data) as OrchestratorEvent
          dispatch({ type: 'event', data })
        } catch {
          dispatch({ type: 'error', message: `Failed to parse ${eventName} payload` })
        }
      })
    }

    eventSource.onopen = () => {
      dispatch({ type: 'connected' })
    }

    eventSource.onerror = () => {
      dispatch({ type: 'disconnected' })
    }
  }, [])

  useEffect(() => {
    connect()

    return () => {
      eventSourceRef.current?.close()
      eventSourceRef.current = null
    }
  }, [connect])

  return sseState
}
