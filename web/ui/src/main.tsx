import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/inter'
import './styles.css'
import App from './App'
import { ErrorBoundary } from './components/ErrorBoundary'
import { AuthProvider } from './lib/auth'
import { PrefsProvider } from './lib/prefs'
import { I18nProvider } from './lib/i18n'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary>
        <I18nProvider>
          <PrefsProvider>
            <AuthProvider>
              <App />
            </AuthProvider>
          </PrefsProvider>
        </I18nProvider>
      </ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)

// The service worker only precaches the app shell; it is skipped in dev so
// Vite's module graph is never served from a stale cache.
if ('serviceWorker' in navigator && import.meta.env.PROD) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // Registration needs HTTPS or localhost. The app works without it.
    })
  })
}
