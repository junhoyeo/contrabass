import { useCallback, useEffect, useReducer, useRef } from 'react'
import type {
  AgentLogEvent,
  BackoffEntry,
  BoardEvent,
  BoardIssue,
  OrchestratorEvent,
  RunningEntry,
  StateSnapshot,
  Stats,
  TeamSnapshot,
  TeamTask,
  WebEvent,
  WorkerState,
} from '../types'

export interface SSEState {
  state: StateSnapshot | null
  connected: boolean
  error: string | null
  teamSnapshot: TeamSnapshot | null
  boardIssues: BoardIssue[]
  agentLogs: AgentLogEvent[]
}

export type SSEAction =
  | { type: 'snapshot'; data: StateSnapshot }
  | { type: 'web_event'; data: WebEvent }
  | { type: 'connected' }
  | { type: 'disconnected' }
  | { type: 'error'; message: string }

interface TeamEventPayload {
  type: string
  team_name: string
  data: Record<string, unknown>
  timestamp: string
}

interface StatusUpdateData {
  Stats: Stats
}

interface AgentStartedData {
  Attempt: number
  PID: number
  SessionID: string
  Workspace: string
}

interface BackoffEnqueuedData {
  Attempt: number
  RetryAt: string
  Error: string
}

interface IssueReleasedData {
  Attempt: number
}

export const INITIAL_STATE: SSEState = {
  state: null,
  connected: false,
  error: null,
  teamSnapshot: null,
  boardIssues: [],
  agentLogs: [],
}

const EMPTY_TEAM_SNAPSHOT: TeamSnapshot = {
  name: '',
  phase: {
    phase: '',
    fix_loop_count: 0,
    transitions: [],
    artifacts: {},
  },
  workers: [],
  tasks: [],
  config: {
    max_workers: 0,
    max_fix_loops: 0,
    claim_lease_seconds: 0,
    state_dir: '',
    agent_type: '',
  },
  created_at: '',
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return {}
  }

  return value as Record<string, unknown>
}

function isWebEvent(value: unknown): value is WebEvent {
  if (typeof value !== 'object' || value === null) {
    return false
  }

  const candidate = value as Partial<WebEvent>
  return (
    typeof candidate.kind === 'string' &&
    typeof candidate.type === 'string' &&
    'payload' in candidate &&
    typeof candidate.timestamp === 'string'
  )
}

function isOrchestratorEvent(value: unknown): value is OrchestratorEvent {
  if (typeof value !== 'object' || value === null) {
    return false
  }

  const candidate = value as Partial<OrchestratorEvent>
  return (
    typeof candidate.Type === 'number' &&
    typeof candidate.IssueID === 'string' &&
    'Data' in candidate &&
    typeof candidate.Timestamp === 'string'
  )
}

function asTeamSnapshot(snapshot: TeamSnapshot | null): TeamSnapshot {
  if (!snapshot) {
    return { ...EMPTY_TEAM_SNAPSHOT, phase: { ...EMPTY_TEAM_SNAPSHOT.phase }, config: { ...EMPTY_TEAM_SNAPSHOT.config } }
  }

  return snapshot
}

function resolveTeamEventPayload(webEvt: WebEvent): TeamEventPayload {
  const payload = asRecord(webEvt.payload)
  const nestedData = asRecord(payload.data)

  return {
    type: typeof payload.type === 'string' ? payload.type : webEvt.type,
    team_name: typeof payload.team_name === 'string' ? payload.team_name : '',
    data: nestedData,
    timestamp: typeof payload.timestamp === 'string' ? payload.timestamp : webEvt.timestamp,
  }
}

function applyTeamEvent(state: SSEState, webEvt: WebEvent): SSEState {
  const teamEvent = resolveTeamEventPayload(webEvt)
  const currentSnapshot = asTeamSnapshot(state.teamSnapshot)

  switch (teamEvent.type) {
    case 'team_created': {
      const config = asRecord(teamEvent.data.config)

      return {
        ...state,
        teamSnapshot: {
          ...currentSnapshot,
          name: teamEvent.team_name || currentSnapshot.name,
          config: {
            ...currentSnapshot.config,
            ...config,
          },
          created_at: teamEvent.timestamp,
        },
      }
    }

    case 'phase_started':
    case 'phase_completed': {
      const transitions = Array.isArray(teamEvent.data.transitions)
        ? (teamEvent.data.transitions as TeamSnapshot['phase']['transitions'])
        : currentSnapshot.phase.transitions
      const rawArtifacts = asRecord(teamEvent.data.artifacts)
      const artifacts = Object.fromEntries(
        Object.entries(rawArtifacts).filter((entry): entry is [string, string] =>
          typeof entry[1] === 'string',
        ),
      )

      return {
        ...state,
        teamSnapshot: {
          ...currentSnapshot,
          name: teamEvent.team_name || currentSnapshot.name,
          phase: {
            ...currentSnapshot.phase,
            phase:
              typeof teamEvent.data.phase === 'string' ? (teamEvent.data.phase as string) : currentSnapshot.phase.phase,
            fix_loop_count:
              typeof teamEvent.data.fix_loop_count === 'number'
                ? teamEvent.data.fix_loop_count
                : currentSnapshot.phase.fix_loop_count,
            transitions,
            artifacts: {
              ...currentSnapshot.phase.artifacts,
              ...artifacts,
            },
          },
        },
      }
    }

    case 'worker_started':
    case 'worker_updated': {
      const worker = teamEvent.data as unknown as WorkerState
      if (!worker?.id) {
        return state
      }

      return {
        ...state,
        teamSnapshot: {
          ...currentSnapshot,
          workers: [...currentSnapshot.workers.filter((entry) => entry.id !== worker.id), worker],
        },
      }
    }

    case 'worker_stopped': {
      const workerID =
        typeof teamEvent.data.worker_id === 'string'
          ? teamEvent.data.worker_id
          : typeof teamEvent.data.id === 'string'
            ? teamEvent.data.id
            : ''
      if (!workerID) {
        return state
      }

      return {
        ...state,
        teamSnapshot: {
          ...currentSnapshot,
          workers: currentSnapshot.workers.filter((entry) => entry.id !== workerID),
        },
      }
    }

    case 'task_created':
    case 'task_updated':
    case 'task_claimed':
    case 'task_completed':
    case 'task_failed': {
      const task = teamEvent.data as unknown as TeamTask
      if (!task?.id) {
        return state
      }

      return {
        ...state,
        teamSnapshot: {
          ...currentSnapshot,
          tasks: [...currentSnapshot.tasks.filter((entry) => entry.id !== task.id), task],
        },
      }
    }

    default:
      return state
  }
}

function applyBoardEvent(state: SSEState, webEvt: WebEvent): SSEState {
  const boardEvent = webEvt.payload as BoardEvent
  if (!boardEvent?.issue) {
    return state
  }

  const issues = [...state.boardIssues]
  const action = boardEvent.action || webEvt.type.replace(/^board_issue_/, '')

  switch (action) {
    case 'created':
      return {
        ...state,
        boardIssues: [...issues.filter((issue) => issue.identifier !== boardEvent.issue.identifier), boardEvent.issue],
      }
    case 'updated':
    case 'moved':
      if (!issues.some((issue) => issue.identifier === boardEvent.issue.identifier)) {
        return { ...state, boardIssues: [...issues, boardEvent.issue] }
      }
      return {
        ...state,
        boardIssues: issues.map((issue) =>
          issue.identifier === boardEvent.issue.identifier ? boardEvent.issue : issue,
        ),
      }
    default:
      return state
  }
}

function applyAgentLogEvent(state: SSEState, webEvt: WebEvent): SSEState {
  const logEvent = webEvt.payload as AgentLogEvent
  if (!logEvent?.worker_id) {
    return state
  }

  const logs = [...state.agentLogs, logEvent]
  if (logs.length > 1000) {
    logs.splice(0, logs.length - 1000)
  }

  return { ...state, agentLogs: logs }
}

export function applyEvent(snapshot: StateSnapshot, event: OrchestratorEvent): StateSnapshot {
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
      const running = snapshot.running.filter((item) => item.issue_id !== event.IssueID)

      return {
        ...snapshot,
        running,
        stats: {
          ...snapshot.stats,
          Running: running.length,
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

export function sseReducer(state: SSEState, action: SSEAction): SSEState {
  switch (action.type) {
    case 'snapshot':
      return { ...state, state: action.data, connected: true, error: null }
    case 'connected':
      return { ...state, connected: true, error: null }
    case 'disconnected':
      return { ...state, connected: false }
    case 'error':
      return { ...state, error: action.message, connected: false }
    case 'web_event': {
      const webEvent = action.data

      switch (webEvent.kind) {
        case 'orchestrator':
          if (!state.state) {
            return state
          }
          return { ...state, state: applyEvent(state.state, webEvent.payload as OrchestratorEvent) }
        case 'team':
          return applyTeamEvent(state, webEvent)
        case 'board':
          return applyBoardEvent(state, webEvent)
        case 'agent_log':
          return applyAgentLogEvent(state, webEvent)
        default:
          return state
      }
    }
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

const TEAM_EVENT_NAMES = [
  'team_created',
  'phase_started',
  'phase_completed',
  'worker_started',
  'worker_updated',
  'worker_stopped',
  'task_created',
  'task_updated',
  'task_claimed',
  'task_completed',
  'task_failed',
] as const

const BOARD_EVENT_NAMES = ['board_issue_created', 'board_issue_updated', 'board_issue_moved'] as const

const AGENT_LOG_EVENT_NAMES = ['agent_log'] as const

const WEB_EVENT_NAMES = [...EVENT_NAMES, ...TEAM_EVENT_NAMES, ...BOARD_EVENT_NAMES, ...AGENT_LOG_EVENT_NAMES] as const

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

    for (const eventName of WEB_EVENT_NAMES) {
      eventSource.addEventListener(eventName, (event) => {
        try {
          const payload = JSON.parse((event as MessageEvent).data) as unknown

          if (isWebEvent(payload)) {
            dispatch({ type: 'web_event', data: payload })
            return
          }

          if (isOrchestratorEvent(payload)) {
            dispatch({
              type: 'web_event',
              data: {
                kind: 'orchestrator',
                type: eventName,
                payload,
                timestamp: payload.Timestamp,
              },
            })
            return
          }

          dispatch({ type: 'error', message: `Failed to parse ${eventName} payload` })
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
