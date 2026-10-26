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
