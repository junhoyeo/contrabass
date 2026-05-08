import { zhCN } from './messages'

const LOCALE = 'zh-CN'

const DASH = '-'

const PHASE_LABELS: Record<number, string> = {
  0: 'PreparingWorkspace',
  1: 'PreparingWorkspace',
  2: 'RunningAgent',
  3: 'RunningAgent',
  4: 'AgentRunning',
  5: 'Releasing',
  6: 'Done',
  7: 'Failed',
  8: 'Failed',
  9: 'Failed',
  10: 'Canceled',
}

const ISSUE_STATE_LABELS: Record<string, string> = {
  open: '待处理',
  in_progress: '进行中',
  done: '已完成',
}

const TEAM_PHASE_LABELS: Record<string, string> = {
  'team-plan': '规划',
  'team-prd': '需求',
  'team-exec': '执行',
  'team-verify': '验证',
  'team-fix': '修复',
  complete: '完成',
  failed: '失败',
  cancelled: '取消',
}

const WORKER_STATUS_LABELS: Record<string, string> = {
  busy: '忙碌',
  idle: '空闲',
  stopped: '已停止',
}

export function formatDuration(totalSeconds: number): string {
  const safeSeconds = Math.max(0, Math.floor(totalSeconds))
  const hours = Math.floor(safeSeconds / 3600)
  const minutes = Math.floor((safeSeconds % 3600) / 60)
  const seconds = safeSeconds % 60

  if (hours > 0) {
    return `${hours}小时 ${minutes}分 ${seconds}秒`
  }

  return `${minutes}分 ${seconds}秒`
}

export function formatElapsedSince(timestamp: string, nowMs = Date.now()): string {
  const started = Date.parse(timestamp)
  if (Number.isNaN(started)) {
    return DASH
  }

  return formatDuration(Math.floor(Math.max(0, nowMs - started) / 1000))
}

export function formatRelativeTime(timestamp: string, nowMs = Date.now()): string {
  const parsed = Date.parse(timestamp)
  if (Number.isNaN(parsed)) {
    return zhCN.sessions.relative.unknown
  }

  const seconds = Math.floor(Math.max(0, nowMs - parsed) / 1000)
  if (seconds < 1) {
    return zhCN.sessions.relative.justNow
  }
  if (seconds < 60) {
    return zhCN.sessions.relative.secondsAgo(seconds)
  }

  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    return zhCN.sessions.relative.minutesAgo(minutes)
  }

  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    return zhCN.sessions.relative.hoursAgo(hours)
  }

  return zhCN.sessions.relative.daysAgo(Math.floor(hours / 24))
}

export function formatDateTime(value: string): string {
  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) {
    return DASH
  }

  return new Date(timestamp).toLocaleString(LOCALE, { hour12: false })
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return zhCN.rateLimits.unknown
  }

  return date.toLocaleTimeString(LOCALE, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

export function formatNumber(value: number): string {
  return value.toLocaleString(LOCALE)
}

export function formatCompactNumber(value: number): string {
  if (value >= 100_000_000) {
    return `${trimTrailingZero(value / 100_000_000)}亿`
  }

  if (value >= 10_000) {
    return `${trimTrailingZero(value / 10_000)}万`
  }

  return formatNumber(value)
}

export function formatPhase(phase: number): string {
  return PHASE_LABELS[phase] ?? `${zhCN.rateLimits.unknown}(${phase})`
}

export function formatIssueState(state: string): string {
  return ISSUE_STATE_LABELS[state] ?? ISSUE_STATE_LABELS.open
}

export function formatTeamPhase(phase: string): string {
  return TEAM_PHASE_LABELS[phase] ?? phase
}

export function formatWorkerStatus(status: string): string {
  return WORKER_STATUS_LABELS[status.toLowerCase()] ?? status
}

function trimTrailingZero(value: number): string {
  return value.toFixed(1).replace(/\\.0$/, '')
}
