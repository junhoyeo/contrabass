import './Header.css'
import { formatDuration } from '../i18n/format'
import { zhCN } from '../i18n/messages'

interface HeaderProps {
  connected: boolean
  runtimeSeconds: number
}

function formatRuntime(runtimeSeconds: number): string {
  return formatDuration(runtimeSeconds)
}

export function Header({ connected, runtimeSeconds }: HeaderProps) {
  return (
    <header className="header">
      <div className="header__brand">
        <img
          src="/contrabass.png"
          alt={zhCN.header.mascotAlt}
          width={48}
          height={48}
          className="header__logo"
        />
        <h1 className="header__title header__title--puffy">Ziikoo</h1>
      </div>

      <div className="header__status">
        <div className={`status-pill ${connected ? 'is-live' : 'is-offline'}`}>
          <span className="status-pill__key">{zhCN.header.status}</span>
          <span className="status-pill__value">
            <span className="status-pill__dot" aria-hidden="true" />
            {connected ? zhCN.header.live : zhCN.header.offline}
          </span>
        </div>
        <div className="status-pill">
          <span className="status-pill__key">{zhCN.header.runtime}</span>
          <span className="status-pill__value">{formatRuntime(runtimeSeconds)}</span>
        </div>
      </div>
    </header>
  )
}
