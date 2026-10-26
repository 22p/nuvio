import { t } from './i18n'
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  getConfig,
  getMe,
  getToken,
  login as apiLogin,
  logoutServer,
  setToken,
  setupServer,
} from './api'
import type { AppConfig, JFUser } from './types'

export interface Session {
  token: string
  user: JFUser
  serverName: string
}

interface AuthContextValue {
  ready: boolean
  config: AppConfig | null
  configError: string
  session: Session | null
  isAdmin: boolean
  signIn: (username: string, password: string) => Promise<void>
  completeSetup: (name: string, password: string) => Promise<void>
  signOut: () => Promise<void>
  refreshConfig: () => Promise<AppConfig | null>
  refreshUser: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false)
  const [config, setConfig] = useState<AppConfig | null>(null)
  const [configError, setConfigError] = useState('')
  const [session, setSession] = useState<Session | null>(null)

  const refreshConfig = async () => {
    try {
      const cfg = await getConfig()
      setConfig(cfg)
      setConfigError('')
      setSession((s) => (s ? { ...s, serverName: cfg.serverName } : s))
      return cfg
    } catch (err) {
      setConfigError(err instanceof Error ? err.message : t("Cannot reach the server"))
      return null
    }
  }

  useEffect(() => {
    let alive = true
    ;(async () => {
      const cfg = await refreshConfig()
      if (!alive) return
      const token = getToken()
      if (token && cfg) {
        try {
          const me = await getMe()
          if (alive) setSession({ token, user: me, serverName: cfg.serverName })
        } catch {
          setToken(null)
        }
      }
      if (alive) setReady(true)
    })()
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const signIn = async (username: string, password: string) => {
    const res = await apiLogin(username, password)
    setToken(res.AccessToken)
    const cfg = await refreshConfig()
    setSession({
      token: res.AccessToken,
      user: res.User,
      serverName: cfg?.serverName || 'Nuvio',
    })
  }

  const completeSetup = async (name: string, password: string) => {
    const res = await setupServer(name, password)
    setToken(res.accessToken)
    const cfg = await refreshConfig()
    setSession({
      token: res.accessToken,
      user: { Id: res.userId, Name: res.userName, Policy: { IsAdministrator: true } },
      serverName: cfg?.serverName || res.serverName || 'Nuvio',
    })
  }

  const signOut = async () => {
    try {
      await logoutServer()
    } catch {
      /* token may already be invalid */
    }
    setToken(null)
    setSession(null)
  }

  const refreshUser = async () => {
    if (!getToken()) return
    const me = await getMe()
    setSession((s) => (s ? { ...s, user: me, serverName: config?.serverName || s.serverName } : s))
  }

  const value = useMemo<AuthContextValue>(
    () => ({
      ready,
      config,
      configError,
      session,
      isAdmin: Boolean(session?.user.Policy?.IsAdministrator),
      signIn,
      completeSetup,
      signOut,
      refreshConfig,
      refreshUser,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [ready, config, configError, session],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
