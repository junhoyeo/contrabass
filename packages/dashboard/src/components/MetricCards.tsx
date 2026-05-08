import type { Stats } from '../types'
import { formatCompactNumber } from '../i18n/format'
import { zhCN } from '../i18n/messages'
import { MetricCard } from './MetricCard'

import './MetricCards.css'

interface MetricCardsProps {
  stats: Stats
  backoffCount: number
}

export function MetricCards({ stats, backoffCount }: MetricCardsProps) {
  const totalTokens = stats.TotalTokensIn + stats.TotalTokensOut

  return (
    <section className="metric-cards" aria-label={zhCN.metrics.ariaLabel}>
      <MetricCard
        title={zhCN.metrics.running}
        value={`${stats.Running}/${stats.MaxAgents}`}
        subtitle={zhCN.metrics.activeAgents}
      />
      <MetricCard title={zhCN.metrics.retrying} value={backoffCount} subtitle={zhCN.metrics.backoffQueue} />
      <MetricCard
        title={zhCN.metrics.totalTokens}
        value={formatCompactNumber(totalTokens)}
        subtitle={zhCN.metrics.tokensInOut(
          formatCompactNumber(stats.TotalTokensIn),
          formatCompactNumber(stats.TotalTokensOut),
        )}
      />
    </section>
  )
}
