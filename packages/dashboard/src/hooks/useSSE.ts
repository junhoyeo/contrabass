import { useCallback, useEffect, useReducer, useRef } from 'react'
import type {
  AgentLogEvent,
  BackoffEntry,
  BoardEvent,
  BoardIssue,
  Issue,
  OrchestratorEvent,
  RunningEntry,
  StateSnapshot,
  Stats,
  TeamSnapshot,
  TeamTask,
  WebEvent,
  WorkerState,
} from '../types'
import { zhCN } from '../i18n/messages'

export interface SSEState {
  state: StateSnapshot | null
  connected: boolean
  error: string | null
  teamSnapshot: TeamSnapshot | null
  boardAvailable: boolean
  boardIssues: BoardIssue[]
  agentLogs: AgentLogEvent[]
  queueEvents: QueueEventPayload[]
  streamWatermark: string | null
}

export type SSEAction =
  | { type: 'snapshot'; data: StateSnapshot; source?: 'stream' | 'refresh' }
  | { type: 'board_snapshot'; data: BoardIssue[] }
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

interface StreamableHTTPMessage {
  jsonrpc: string
  id?: string | number | null
  method?: string
  params?: unknown
  result?: unknown
  error?: {
    code: number
    message: string
  }
}

export interface QueueEventPayload {
  issue_id: string
  identifier: string
  blockers: string
}

export const INITIAL_STATE: SSEState = {
  state: null,
  connected: false,
  error: null,
  teamSnapshot: null,
  boardAvailable: false,
  boardIssues: [],
  agentLogs: [],
  queueEvents: [],
  streamWatermark: null,
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

const MAX_BUFFERED_ENTRIES = 1000
const MAX_STREAM_BUFFER_BYTES = 1 << 20
const STREAM_ENDPOINT = '/api/v1/stream'
const STREAM_RECONNECT_BASE_DELAY_MS = 500
const STREAM_RECONNECT_MAX_DELAY_MS = 5000
const STREAM_SUBSCRIBE_REQUEST = {
  jsonrpc: '2.0',
  id: 'dashboard-subscribe',
  method: 'dashboard.subscribe',
}


function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return {}
  }

  return value as Record<string, unknown>
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
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

function isStats(value: unknown): value is Stats {
  if (!isRecord(value)) {
    return false
  }

  return (
    typeof value.Running === 'number' &&
    typeof value.MaxAgents === 'number' &&
    typeof value.TotalTokensIn === 'number' &&
    typeof value.TotalTokensOut === 'number' &&
    typeof value.StartTime === 'string' &&
    typeof value.PollCount === 'number'
  )
}

function isRunningEntry(value: unknown): value is RunningEntry {
  if (!isRecord(value)) {
    return false
  }

  return (
    typeof value.issue_id === 'string' &&
    typeof value.attempt === 'number' &&
    typeof value.pid === 'number' &&
    typeof value.session_id === 'string' &&
    typeof value.workspace === 'string' &&
    typeof value.started_at === 'string' &&
    typeof value.phase === 'number' &&
    typeof value.tokens_in === 'number' &&
    typeof value.tokens_out === 'number'
  )
}

function isBackoffEntry(value: unknown): value is BackoffEntry {
  if (!isRecord(value)) {
    return false
  }

  return (
    typeof value.issue_id === 'string' &&
    typeof value.attempt === 'number' &&
    typeof value.retry_at === 'string' &&
    typeof value.error === 'string'
  )
}

function isNullableStringArray(value: unknown): boolean {
  return value === null || (Array.isArray(value) && value.every((entry) => typeof entry === 'string'))
}

function isIssueRecord(value: unknown): value is Issue {
  if (!isRecord(value)) {
    return false
  }

  return (
    typeof value.id === 'string' &&
    typeof value.title === 'string' &&
    typeof value.description === 'string' &&
    typeof value.state === 'number' &&
    (!('identifier' in value) || typeof value.identifier === 'string') &&
    (!('labels' in value) || isNullableStringArray(value.labels)) &&
    (!('blocked_by' in value) || isNullableStringArray(value.blocked_by)) &&
    (!('tracker_meta' in value) || value.tracker_meta === null || isRecord(value.tracker_meta))
  )
}

function isIssueMap(value: unknown): value is StateSnapshot['issues'] {
  if (!isRecord(value)) {
    return false
  }

  return Object.values(value).every(isIssueRecord)
}

function normalizeStateSnapshot(value: unknown): StateSnapshot | null {
  if (!isRecord(value)) {
    return null
  }

  if (typeof value.generated_at !== 'string' || !isStats(value.stats)) {
    return null
  }

  const running =
    value.running === null ? [] : Array.isArray(value.running) && value.running.every(isRunningEntry) ? value.running : null
  const backoff =
    value.backoff === null ? [] : Array.isArray(value.backoff) && value.backoff.every(isBackoffEntry) ? value.backoff : null
  const issues = value.issues === null ? {} : isIssueMap(value.issues) ? value.issues : null

  if (!running || !backoff || !issues) {
    return null
  }

  return {
    ...value,
    stats: value.stats,
    running,
    backoff,
    issues,
    generated_at: value.generated_at,
  }
}

function isStreamableHTTPMessage(value: unknown): value is StreamableHTTPMessage {
  if (typeof value !== 'object' || value === null) {
    return false
  }

  const candidate = value as Partial<StreamableHTTPMessage>
  return candidate.jsonrpc === '2.0'
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function sseFrameData(frame: string): string | null {
  const dataLines = frame
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trimStart())

  if (dataLines.length === 0) {
    return null
  }

  return dataLines.join('\n')
}

function streamBufferBytes(buffer: string): number {
  return new TextEncoder().encode(buffer).byteLength
}

function consumeSSEBuffer(buffer: string, onMessage: (message: StreamableHTTPMessage) => void): string {
  let nextBuffer = buffer.replace(/\r\n/g, '\n')
  let boundary = nextBuffer.indexOf('\n\n')

  while (boundary >= 0) {
    const frame = nextBuffer.slice(0, boundary)
    if (streamBufferBytes(frame) > MAX_STREAM_BUFFER_BYTES) {
      throw new Error('stream frame is too large')
    }
    nextBuffer = nextBuffer.slice(boundary + 2)

    const data = sseFrameData(frame)
    if (data !== null) {
      const parsed = JSON.parse(data) as unknown
      if (!isStreamableHTTPMessage(parsed)) {
        throw new Error('invalid streamable HTTP message')
      }
      onMessage(parsed)
    }

    boundary = nextBuffer.indexOf('\n\n')
  }

  if (streamBufferBytes(nextBuffer) > MAX_STREAM_BUFFER_BYTES) {
    throw new Error('stream buffer is too large')
  }

  return nextBuffer
}

async function readStreamableHTTPResponse(
  response: Response,
  onMessage: (message: StreamableHTTPMessage) => void,
): Promise<void> {
  const contentType = response.headers.get('Content-Type') ?? ''
  if (contentType.includes('application/json')) {
    const parsed = (await response.json()) as unknown
    if (!isStreamableHTTPMessage(parsed)) {
      throw new Error('invalid streamable HTTP response')
    }
    onMessage(parsed)
    return
  }

  if (!contentType.includes('text/event-stream')) {
    throw new Error('unsupported streamable HTTP response content type')
  }

  if (!response.body) {
    throw new Error('streamable HTTP response body is missing')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) {
        break
      }

      buffer = consumeSSEBuffer(buffer + decoder.decode(value, { stream: true }), onMessage)
    }

    buffer = consumeSSEBuffer(buffer + decoder.decode(), onMessage)
  } finally {
    reader.releaseLock()
  }
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

function appendAgentLog(state: SSEState, logEvent: AgentLogEvent): SSEState {
  const logs = [...state.agentLogs, logEvent]
  if (logs.length > MAX_BUFFERED_ENTRIES) {
    logs.splice(0, logs.length - MAX_BUFFERED_ENTRIES)
  }

  return { ...state, agentLogs: logs }
}

function describeTeamEvent(teamEvent: TeamEventPayload): string {
  const data = teamEvent.data
  let toolName = ''
  if (typeof data.tool_name === 'string') {
    toolName = data.tool_name
  } else if (typeof data.name === 'string') {
    toolName = data.name
  } else if (typeof data.tool === 'string') {
    toolName = data.tool
  }

  if (teamEvent.type === 'tool_call' && toolName) {
    return `tool_call: ${toolName}`
  }

  return teamEvent.type
}

function applyTeamLogEvent(state: SSEState, teamEvent: TeamEventPayload): SSEState {
  return appendAgentLog(state, {
    worker_id: teamEvent.team_name || 'team',
    line: describeTeamEvent(teamEvent),
    stream: 'team',
    timestamp: teamEvent.timestamp,
  })
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
      return applyTeamLogEvent(state, teamEvent)
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
        boardAvailable: true,
        boardIssues: [...issues.filter((issue) => issue.identifier !== boardEvent.issue.identifier), boardEvent.issue],
      }
    case 'updated':
    case 'moved':
      if (!issues.some((issue) => issue.identifier === boardEvent.issue.identifier)) {
        return { ...state, boardAvailable: true, boardIssues: [...issues, boardEvent.issue] }
      }
      return {
        ...state,
        boardAvailable: true,
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

  return appendAgentLog(state, logEvent)
}

function applyQueueEvent(state: SSEState, webEvt: WebEvent): SSEState {
  const payload = asRecord(webEvt.payload)
  const issueID = typeof payload.issue_id === 'string' ? payload.issue_id : ''
  if (!issueID) {
    return state
  }

  const entry: QueueEventPayload = {
    issue_id: issueID,
    identifier: typeof payload.identifier === 'string' ? payload.identifier : issueID,
    blockers: typeof payload.blockers === 'string' ? payload.blockers : '',
  }

  const queueEvents = [...state.queueEvents, entry]
  if (queueEvents.length > MAX_BUFFERED_ENTRIES) {
    queueEvents.splice(0, queueEvents.length - MAX_BUFFERED_ENTRIES)
  }

  return { ...state, queueEvents }
}

function boardIssueTimestamp(issue: BoardIssue): number {
  const parsed = Date.parse(issue.updated_at)
  return Number.isNaN(parsed) ? Number.NEGATIVE_INFINITY : parsed
}

function mergeBoardSnapshot(current: BoardIssue[], snapshot: BoardIssue[]): BoardIssue[] {
  const merged = new Map<string, BoardIssue>()

  for (const issue of snapshot) {
    merged.set(issue.identifier || issue.id, issue)
  }

  for (const issue of current) {
    const key = issue.identifier || issue.id
    const existing = merged.get(key)
    if (existing && boardIssueTimestamp(issue) > boardIssueTimestamp(existing)) {
      merged.set(key, issue)
    }
  }

  return Array.from(merged.values())
}

function snapshotTimestamp(snapshot: StateSnapshot | null): number {
  if (!snapshot?.generated_at) {
    return Number.NEGATIVE_INFINITY
  }
  const parsed = Date.parse(snapshot.generated_at)
  return Number.isNaN(parsed) ? Number.NEGATIVE_INFINITY : parsed
}

function timestampFromString(value: string | null): number {
  if (!value) {
    return Number.NEGATIVE_INFINITY
  }
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? Number.NEGATIVE_INFINITY : parsed
}

function shouldAcceptSnapshot(current: StateSnapshot | null, next: StateSnapshot, watermark: string | null): boolean {
  const currentTimestamp = Math.max(snapshotTimestamp(current), timestampFromString(watermark))
  return snapshotTimestamp(next) >= currentTimestamp
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
    case 'snapshot': {
      const streamSnapshot = action.source !== 'refresh'
      const watermark = streamSnapshot ? null : state.streamWatermark
      if (!shouldAcceptSnapshot(state.state, action.data, watermark)) {
        return streamSnapshot ? { ...state, connected: true, error: null } : state
      }
      return {
        ...state,
        state: action.data,
        connected: streamSnapshot ? true : state.connected,
        error: streamSnapshot ? null : state.error,
        streamWatermark: streamSnapshot ? action.data.generated_at : state.streamWatermark,
      }
    }
    case 'board_snapshot':
      return {
        ...state,
        boardAvailable: true,
        boardIssues: mergeBoardSnapshot(state.boardIssues, action.data),
      }
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
          if (webEvent.type === 'dispatch_skipped_blocked_by') {
            return applyQueueEvent(state, webEvent)
          }
          if (!state.state) {
            return state
          }
          return {
            ...state,
            state: applyEvent(state.state, webEvent.payload as OrchestratorEvent),
            streamWatermark: webEvent.timestamp,
          }
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

export function useSSE() {
  const [sseState, dispatch] = useReducer(sseReducer, INITIAL_STATE)
  const streamControllerRef = useRef<AbortController | null>(null)
  const refreshControllerRef = useRef<AbortController | null>(null)
  const reconnectTimerRef = useRef<number | null>(null)
  const reconnectAttemptRef = useRef(0)
  const connectRef = useRef<() => void>(() => {})

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
  }, [])

  const scheduleReconnect = useCallback((controller: AbortController) => {
    if (controller.signal.aborted || reconnectTimerRef.current !== null) {
      return
    }

    const attempt = reconnectAttemptRef.current
    reconnectAttemptRef.current += 1
    const delay = Math.min(
      STREAM_RECONNECT_BASE_DELAY_MS * 2 ** attempt,
      STREAM_RECONNECT_MAX_DELAY_MS,
    )

    reconnectTimerRef.current = window.setTimeout(() => {
      reconnectTimerRef.current = null
      if (!controller.signal.aborted) {
        connectRef.current()
      }
    }, delay)
  }, [])

  const refreshBoardIssues = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await fetch('/api/v1/board/issues', { signal })
      if (response.status === 404) {
        return
      }
      if (!response.ok) {
        return
      }
      const data = (await response.json()) as BoardIssue[]
      dispatch({ type: 'board_snapshot', data })
    } catch (error) {
      if (isAbortError(error)) {
        return
      }
      // Board support is optional; stream connection state reports core dashboard health.
    }
  }, [])

  const connect = useCallback(() => {
    clearReconnectTimer()
    streamControllerRef.current?.abort()
    const controller = new AbortController()
    streamControllerRef.current = controller

    const handleMessage = (message: StreamableHTTPMessage) => {
      if (message.error) {
        dispatch({ type: 'error', message: message.error.message })
        return
      }

      switch (message.method) {
        case 'dashboard.snapshot':
          {
            const snapshot = normalizeStateSnapshot(message.params)
            if (snapshot) {
              dispatch({ type: 'snapshot', data: snapshot, source: 'stream' })
            } else {
              dispatch({ type: 'error', message: zhCN.errors.parseSnapshot })
            }
          }
          return
        case 'dashboard.event':
          if (isWebEvent(message.params)) {
            dispatch({ type: 'web_event', data: message.params })
          } else {
            dispatch({ type: 'error', message: zhCN.errors.parseEvent('dashboard.event') })
          }
          return
        default:
          // JSON-RPC responses to dashboard.subscribe/ping are acknowledgements.
          return
      }
    }

    void (async () => {
      try {
        const response = await fetch(STREAM_ENDPOINT, {
          method: 'POST',
          headers: {
            Accept: 'application/json, text/event-stream',
            'Content-Type': 'application/json',
            'Mcp-Protocol-Version': '2025-06-18',
          },
          body: JSON.stringify(STREAM_SUBSCRIBE_REQUEST),
          signal: controller.signal,
        })

        if (!response.ok) {
          dispatch({ type: 'error', message: `stream failed (${response.status})` })
          scheduleReconnect(controller)
          return
        }

        dispatch({ type: 'connected' })
        reconnectAttemptRef.current = 0
        await readStreamableHTTPResponse(response, handleMessage)
        dispatch({ type: 'disconnected' })
        scheduleReconnect(controller)
      } catch (error) {
        if (isAbortError(error)) {
          return
        }
        dispatch({ type: 'error', message: error instanceof Error ? error.message : 'stream failed' })
        scheduleReconnect(controller)
      } finally {
        if (streamControllerRef.current === controller) {
          streamControllerRef.current = null
        }
      }
    })()
  }, [clearReconnectTimer, scheduleReconnect])

  useEffect(() => {
    connectRef.current = connect
  }, [connect])

  useEffect(() => {
    connect()

    return () => {
      streamControllerRef.current?.abort()
      streamControllerRef.current = null
      clearReconnectTimer()
    }
  }, [connect, clearReconnectTimer])

  useEffect(() => {
    const controller = new AbortController()
    void refreshBoardIssues(controller.signal)
    return () => controller.abort()
  }, [refreshBoardIssues])

  // Per-issue tokens (running[i].tokens_in/out) and diff stats are only
  // populated in the initial /api/v1/state snapshot — the StatusUpdate SSE
  // event carries top-level Stats only. Periodically refetch the full
  // snapshot so per-row numbers stay live without a manual page reload.
  const refresh = useCallback(async () => {
    refreshControllerRef.current?.abort()
    const controller = new AbortController()
    refreshControllerRef.current = controller

    try {
      const response = await fetch('/api/v1/state', { signal: controller.signal })
      if (!response.ok) {
        return
      }
      const data = (await response.json()) as unknown
      const snapshot = normalizeStateSnapshot(data)
      if (snapshot) {
        dispatch({ type: 'snapshot', data: snapshot, source: 'refresh' })
      }
    } catch (error) {
      if (isAbortError(error)) {
        return
      }
      // Silent: stream will surface connection issues separately.
    } finally {
      if (refreshControllerRef.current === controller) {
        refreshControllerRef.current = null
      }
    }
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => {
      void refresh()
    }, 5000)
    return () => {
      window.clearInterval(timer)
      refreshControllerRef.current?.abort()
      refreshControllerRef.current = null
    }
  }, [refresh])

  return { ...sseState, refresh }
}
