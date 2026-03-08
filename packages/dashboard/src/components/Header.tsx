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
        <img
          src="/contrabass.png"
          alt="Contrabass mascot"
          width={48}
          height={48}
          className="header__logo"
        />
        <h1 className="header__title header__title--puffy">Contrabass</h1>
      </div>

      <div className="header__status">
        <div className={`status-pill ${connected ? 'is-live' : 'is-offline'}`}>
          <span className="status-pill__key">Status</span>
          <span className="status-pill__value">
            <span className="status-pill__dot" aria-hidden="true" />
            {connected ? 'Live' : 'Offline'}
          </span>
        </div>
        <div className="status-pill">
          <span className="status-pill__key">Runtime</span>
          <span className="status-pill__value">{formatRuntime(runtimeSeconds)}</span>
        </div>
      </div>
    </header>
  )
}
