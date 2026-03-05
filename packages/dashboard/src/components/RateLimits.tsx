import './RateLimits.css'

interface RateLimit {
  name: string
  remaining: number
  resetAt: string
}

interface RateLimitsProps {
  limits: RateLimit[]
}

function formatResetTime(resetAt: string): string {
  const date = new Date(resetAt)
  if (Number.isNaN(date.getTime())) {
    return 'Unknown'
  }

  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export function RateLimits({ limits }: RateLimitsProps) {
  if (limits.length === 0) {
    return (
      <section className="rate-limits rate-limits--empty" aria-live="polite">
        <p className="rate-limits__empty-text">No rate limits active</p>
      </section>
    )
  }

  return (
    <section className="rate-limits" aria-label="Rate limits">
      {limits.map((limit) => (
        <dl className="rate-limits__item" key={limit.name}>
          <div className="rate-limits__row">
            <dt>Limit</dt>
            <dd>{limit.name}</dd>
          </div>
          <div className="rate-limits__row">
            <dt>Remaining</dt>
            <dd className="rate-limits__mono">{limit.remaining}</dd>
          </div>
          <div className="rate-limits__row">
            <dt>Reset</dt>
            <dd className="rate-limits__mono">{formatResetTime(limit.resetAt)}</dd>
          </div>
        </dl>
      ))}
    </section>
  )
}
