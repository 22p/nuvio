import { t } from '../lib/i18n'
import { useState, type FormEvent, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../lib/auth'
import { Logo } from '../components/Layout'
import { LangSwitch } from '../components/LangSwitch'

export function SetupPage() {
  const { completeSetup, config } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    if (name.trim().length < 1) return setError(t("Enter a username"))
    if (password.length < 4) return setError(t("Password must be at least 4 characters"))
    if (password !== confirm) return setError(t("The passwords do not match"))
    setBusy(true)
    try {
      await completeSetup(name.trim(), password)
      navigate(location.pathname === '/' ? '/' : location.pathname + location.search, { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Setup failed"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthLayout
      eyebrow={t("First run")}
      title={t("Create administrator account")}
      subtitle={t("Set the account used to sign in to the server and clients.")}
    >
      <form onSubmit={submit} className="auth-form">
        <label className="field">
          <span>{t("Administrator username")}</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            autoComplete="username"
            placeholder={t("e.g. nuvio")}
          />
        </label>
        <label className="field">
          <span>{t("Password")}</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            placeholder={t("At least 4 characters")}
          />
        </label>
        <label className="field">
          <span>{t("Confirm password")}</span>
          <input
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            placeholder={t("Enter again")}
          />
        </label>
        {error && <div className="form-error">{t(error)}</div>}
        <button className="btn btn-primary btn-block" disabled={busy}>
          {busy ? t("Creating…") : t("Create and continue")}
        </button>
        <p className="form-note">
          {config?.demo
            ? t("The demo library is ready. You can add real folders in Settings at any time.")
            : t("After setup you can add folders and scan media libraries in Settings.")}
          {config?.ffmpeg === false && t(" ffmpeg was not found. Formats that cannot be played directly will not be transcoded.")}
        </p>
      </form>
    </AuthLayout>
  )
}

export function AuthLayout({
  eyebrow,
  title,
  subtitle,
  children,
}: {
  eyebrow: string
  title: string
  subtitle: string
  children: ReactNode
}) {
  return (
    <div className="auth-page">
      <div className="auth-glow glow-a" />
      <div className="auth-glow glow-b" />
      <LangSwitch />
      <div className="auth-brand-fixed">
        <Logo size={30} />
        <span>NUVIO</span>
      </div>
      <main className="auth-panel">
        <div className="auth-card">
          <div className="eyebrow">{eyebrow}</div>
          <h2>{title}</h2>
          <p className="auth-sub">{subtitle}</p>
          {children}
        </div>
        <div className="auth-foot">Nuvio</div>
      </main>
    </div>
  )
}
