import { useEffect, useMemo, useRef, useState } from 'react'
import type { AgentLogEvent } from '../types'
import './AgentLogs.css'

interface AgentLogsProps {
  logs: AgentLogEvent[]
}

const WORKER_TONE_COUNT = 5
const MAX_VISIBLE_LOGS = 500

function formatTimestamp(timestamp: string): string {
  if (timestamp.length >= 19 && timestamp.includes('T')) {
    return timestamp.slice(11, 19)
  }

  const parsed = Date.parse(timestamp)
  if (Number.isNaN(parsed)) {
    return '--:--:--'
  }

  const date = new Date(parsed)
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  const seconds = String(date.getSeconds()).padStart(2, '0')
  return `${hours}:${minutes}:${seconds}`
}

function getWorkerTone(workerID: string): number {
  let hash = 0
  for (let index = 0; index < workerID.length; index += 1) {
    hash = (hash * 31 + workerID.charCodeAt(index)) >>> 0
  }

  return hash % WORKER_TONE_COUNT
}

export function AgentLogs({ logs }: AgentLogsProps) {
  const [selectedWorker, setSelectedWorker] = useState('all')
  const [shouldAutoScroll, setShouldAutoScroll] = useState(true)
  const viewportRef = useRef<HTMLDivElement | null>(null)

  const workerIDs = useMemo(() => {
    const unique = Array.from(new Set(logs.map((log) => log.worker_id)))
    return unique.sort((a, b) => a.localeCompare(b))
  }, [logs])

  useEffect(() => {
    if (selectedWorker !== 'all' && !workerIDs.includes(selectedWorker)) {
      setSelectedWorker('all')
    }
  }, [selectedWorker, workerIDs])

  const filteredLogs = useMemo(() => {
    if (selectedWorker === 'all') {
      return logs
    }

    return logs.filter((log) => log.worker_id === selectedWorker)
  }, [logs, selectedWorker])

  const visibleLogs = useMemo(() => {
    if (filteredLogs.length <= MAX_VISIBLE_LOGS) {
      return filteredLogs
    }

    return filteredLogs.slice(-MAX_VISIBLE_LOGS)
  }, [filteredLogs])

  useEffect(() => {
    const viewport = viewportRef.current
    if (viewport === null || !shouldAutoScroll) {
      return
    }

    viewport.scrollTop = viewport.scrollHeight
  }, [visibleLogs, shouldAutoScroll])

  function handleScroll() {
    const viewport = viewportRef.current
    if (viewport === null) {
      return
    }

    const bottomOffset = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight
    setShouldAutoScroll(bottomOffset <= 8)
  }

  if (logs.length === 0) {
    return (
      <section className="agent-logs agent-logs--empty" aria-live="polite">
        <p className="agent-logs__empty-text">No agent logs</p>
      </section>
    )
  }

  return (
    <section className="agent-logs" aria-label="Agent logs">
      <header className="agent-logs__header">
        <h3 className="agent-logs__title">Agent Logs</h3>
        <label className="agent-logs__filter-label" htmlFor="agent-logs-worker-filter">
          Worker
        </label>
        <select
          id="agent-logs-worker-filter"
          className="agent-logs__filter"
          value={selectedWorker}
          onChange={(event) => setSelectedWorker(event.target.value)}
        >
          <option value="all">All</option>
          {workerIDs.map((workerID) => (
            <option key={workerID} value={workerID}>
              {workerID}
            </option>
          ))}
        </select>
      </header>

      <div className="agent-logs__viewport" ref={viewportRef} onScroll={handleScroll}>
        {visibleLogs.length === 0 ? (
          <p className="agent-logs__empty-filtered">No logs for selected worker</p>
        ) : (
          visibleLogs.map((log, index) => {
            const tone = getWorkerTone(log.worker_id)

            return (
              <div
                key={`${log.worker_id}-${log.timestamp}-${index}`}
                className={`agent-logs__line ${log.stream === 'stderr' ? 'agent-logs__line--stderr' : ''}`}
                title={log.line}
              >
                <span className="agent-logs__timestamp">[{formatTimestamp(log.timestamp)}]</span>
                <span className={`agent-logs__worker agent-logs__worker--tone-${tone}`}>[{log.worker_id}]</span>
                <span className="agent-logs__message">{log.line}</span>
              </div>
            )
          })
        )}
      </div>
    </section>
  )
}

export default AgentLogs
