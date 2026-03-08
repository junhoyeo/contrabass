import type { Stats } from '../types'
import { MetricCard } from './MetricCard'

import './MetricCards.css'

interface MetricCardsProps {
  stats: Stats
  backoffCount: number
}

function formatCompactNumber(value: number): string {
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(1)}B`
  }

  if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(1)}M`
  }

  if (value >= 1_000) {
    return `${(value / 1_000).toFixed(1)}K`
  }

  return value.toString()
}

export function MetricCards({ stats, backoffCount }: MetricCardsProps) {
  const totalTokens = stats.TotalTokensIn + stats.TotalTokensOut

  return (
    <section className="metric-cards" aria-label="Dashboard metrics">
      <MetricCard
        title="Running"
        value={`${stats.Running}/${stats.MaxAgents}`}
        subtitle="Active agents"
      />
      <MetricCard title="Retrying" value={backoffCount} subtitle="Backoff queue" />
      <MetricCard
        title="Total Tokens"
        value={formatCompactNumber(totalTokens)}
        subtitle={`${formatCompactNumber(stats.TotalTokensIn)} in / ${formatCompactNumber(stats.TotalTokensOut)} out`}
      />
    </section>
  )
}
