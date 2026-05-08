import './RateLimits.css'
import { formatTime } from '../i18n/format'
import { zhCN } from '../i18n/messages'

interface RateLimit {
  name: string
  remaining: number
  resetAt: string
}

interface RateLimitsProps {
  limits: RateLimit[]
}

function formatResetTime(resetAt: string): string {
  return formatTime(resetAt)
}

export function RateLimits({ limits }: RateLimitsProps) {
  if (limits.length === 0) {
    return (
      <section className="rate-limits rate-limits--empty" aria-live="polite">
        <p className="rate-limits__empty-text">{zhCN.rateLimits.empty}</p>
      </section>
    )
  }

  return (
    <section className="rate-limits" aria-label={zhCN.rateLimits.ariaLabel}>
      {limits.map((limit) => (
        <dl className="rate-limits__item" key={limit.name}>
          <div className="rate-limits__row">
            <dt>{zhCN.rateLimits.labels.limit}</dt>
            <dd>{limit.name}</dd>
          </div>
          <div className="rate-limits__row">
            <dt>{zhCN.rateLimits.labels.remaining}</dt>
            <dd className="rate-limits__mono">{limit.remaining}</dd>
          </div>
          <div className="rate-limits__row">
            <dt>{zhCN.rateLimits.labels.reset}</dt>
            <dd className="rate-limits__mono">{formatResetTime(limit.resetAt)}</dd>
          </div>
        </dl>
      ))}
    </section>
  )
}
