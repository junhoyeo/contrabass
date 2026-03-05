import './Header.css'

interface HeaderProps {
  connected: boolean
  runtimeSeconds: number
}

function formatRuntime(runtimeSeconds: number): string {
  const safeSeconds = Math.max(0, Math.floor(runtimeSeconds))
  const minutes = Math.floor(safeSeconds / 60)
  const seconds = safeSeconds % 60

  return `${minutes}m ${seconds}s`
}

export function Header({ connected, runtimeSeconds }: HeaderProps) {
  return (
    <header className="header">
      <div className="header__brand">
        <h1 className="header__title">Contrabass</h1>
      </div>

      <div className="header__status">
        <div className={`status-badge ${connected ? 'is-live' : 'is-offline'}`}>
          <span className="status-badge__dot" aria-hidden="true" />
          <span>{connected ? 'Live' : 'Offline'}</span>
        </div>
        <span className="header__runtime">Runtime {formatRuntime(runtimeSeconds)}</span>
      </div>
    </header>
  )
}
