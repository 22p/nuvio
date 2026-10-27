import { t } from './lib/i18n'
import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { useAuth } from './lib/auth'
import { AppShell, Logo } from './components/Layout'
import { HomePage } from './pages/Home'
import { BrowsePage } from './pages/Browse'
import { FavoritesPage } from './pages/Favorites'
import { LibraryPage } from './pages/Library'
import { DetailPage } from './pages/Detail'
import { SearchPage } from './pages/Search'
import { SettingsPage } from './pages/Settings'
import { UsersPage } from './pages/Users'
import { SetupPage } from './pages/Setup'
import { LoginPage } from './pages/Login'
import { Spinner } from './components/Media'

const PlayerPage = lazy(() =>
  import('./pages/Player').then((m) => ({ default: m.PlayerPage })),
)

export default function App() {
  const { ready, config, configError, session, refreshConfig } = useAuth()

  if (!ready) return <Splash />
  if (configError && !config) return <ConnectionError message={configError} retry={refreshConfig} />
  if (!config?.setupComplete) return <SetupPage />
  if (!session) return <LoginPage />

  return (
    <Routes>
      <Route
        path="/watch/:id"
        element={
          <Suspense fallback={<Splash />}>
            <PlayerPage />
          </Suspense>
        }
      />
      <Route element={<AppShell />}>
        <Route path="/" element={<HomePage />} />
        <Route path="/browse" element={<BrowsePage />} />
        <Route path="/favorites" element={<FavoritesPage />} />
        <Route path="/library/:id" element={<LibraryPage />} />
        <Route path="/item/:id" element={<DetailPage />} />
        <Route path="/search" element={<SearchPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

function Splash() {
  return (
    <div className="splash">
      <div className="splash-glow" />
      <Logo size={52} />
      <div className="splash-name">NUVIO</div>
      <Spinner />
    </div>
  )
}

function ConnectionError({ message, retry }: { message: string; retry: () => Promise<unknown> }) {
  return (
    <div className="splash">
      <div className="splash-glow" />
      <Logo size={46} />
      <div className="splash-name">NUVIO</div>
      <div className="connection-error">
        <h2>{t("Cannot reach the server")}</h2>
        <p>{message}</p>
        <button className="btn btn-ghost" onClick={() => retry()}>{t("Retry")}</button>
      </div>
    </div>
  )
}
