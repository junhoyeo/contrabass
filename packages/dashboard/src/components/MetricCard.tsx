import './MetricCard.css'

interface MetricCardProps {
  title: string
  value: string | number
  subtitle?: string
}

export function MetricCard({ title, value, subtitle }: MetricCardProps) {
  return (
    <article className="metric-card">
      <p className="metric-card__title">{title}</p>
      <p className="metric-card__value">{value}</p>
      {subtitle ? <p className="metric-card__subtitle">{subtitle}</p> : null}
    </article>
  )
}
