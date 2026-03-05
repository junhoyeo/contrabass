import './App.css'
import { Header } from './components/Header'
import { MetricCards } from './components/MetricCards'
import type { Stats } from './types'

const mockStats: Stats = {
  Running: 3,
  MaxAgents: 8,
  TotalTokensIn: 1_240_000,
  TotalTokensOut: 870_000,
  StartTime: '2026-03-05T12:00:00Z',
  PollCount: 96,
}

function App() {
  const runtimeSeconds = 18 * 60 + 27

  return (
    <div className="dashboard">
      <Header connected runtimeSeconds={runtimeSeconds} />
      <MetricCards stats={mockStats} backoffCount={2} runtimeSeconds={runtimeSeconds} />
    </div>
  )
}

export default App
