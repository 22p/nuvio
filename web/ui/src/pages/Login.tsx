import { t } from '../lib/i18n'
import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../lib/auth'
import { AuthLayout } from './Setup'
import { IconCheck, IconLayers } from '../components/Icons'

export function LoginPage() {
  const { signIn, config } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const address = window.location.origin

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await signIn(name.trim(), password)
      navigate(location.pathname === '/' ? '/' : location.pathname + location.search, { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Sign-in failed"))
    } finally {
      setBusy(false)
    }
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      /* clipboard unavailable */
    }
  }

  return (
    <AuthLayout
      eyebrow={t("Sign in")}
      title={config?.serverName || 'Nuvio'}
      subtitle={t("Sign in with your server account.")}
    >
      <form onSubmit={submit} className="auth-form">
        <label className="field">
          <span>{t("Username")}</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            autoComplete="username"
            placeholder={t("Username")}
          />
        </label>
        <label className="field">
          <span>{t("Password")}</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            placeholder={t("Password")}
          />
        </label>
        {error && <div className="form-error">{t(error)}</div>}
        <button className="btn btn-primary btn-block" disabled={busy}>
          {busy ? t("Signing in…") : t("Sign in")}
        </button>
      </form>

      <div className="server-address">
        <div className="server-address-head">
          <IconLayers size={16} />
          <span>{t("Server address")}</span>
        </div>
        <button className="address-row" onClick={copy} title={t("Click to copy")}>
          <code>{address}</code>
          <span className="copy-hint">{copied ? <IconCheck size={15} /> : t("Copy")}</span>
        </button>
        <p>{t("Add this address in your Jellyfin client and sign in with the same account.")}</p>
      </div>

      <div className="version-line">
        {config?.version ? t("Nuvio {0} · API {1}", { 0: config.version, 1: config.apiVersion }) : 'Nuvio'}
      </div>
    </AuthLayout>
  )
}
