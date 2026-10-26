import { t } from '../lib/i18n'
import { Component, type ErrorInfo, type ReactNode } from 'react'

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Nuvio UI error', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="splash">
          <div className="splash-glow" />
          <div className="connection-error">
            <h2>{t("Something went wrong")}</h2>
            <p>{this.state.error.message}</p>
            <button className="btn btn-ghost" onClick={() => window.location.reload()}>{t("Reload")}</button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
