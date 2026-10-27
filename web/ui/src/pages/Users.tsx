import { t } from '../lib/i18n'
import { useEffect, useState, type FormEvent } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { createUser, deleteUser, getAdminOverview, updateUser } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { AdminOverview, AdminUser, LibraryInfo } from '../lib/types'
import { Spinner } from '../components/Media'
import { Switch } from '../components/Controls'
import { IconChevronLeft, IconTrash, IconUser } from '../components/Icons'

export function UsersPage() {
  const { session, isAdmin } = useAuth()
  const [overview, setOverview] = useState<AdminOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [newUser, setNewUser] = useState('')
  const [newPass, setNewPass] = useState('')
  const [newAdmin, setNewAdmin] = useState(false)

  const load = async () => {
    try {
      const data = await getAdminOverview()
      setOverview(data)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Could not load users"))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (isAdmin) load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin])

  if (!isAdmin) return <Navigate to="/settings" replace />

  const submitUser = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setNotice('')
    try {
      await createUser(newUser.trim(), newPass, newAdmin)
      setNewUser('')
      setNewPass('')
      setNewAdmin(false)
      setNotice(t("User created. They can now sign in from a Jellyfin client."))
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Creation failed"))
    }
  }

  return (
    <div className="page-pad settings-page users-page">
      <div className="page-heading">
        <div>
          <div className="eyebrow">{t("Server")}</div>
          <h1>{t("Users")}</h1>
          <p className="muted">{t("Set the libraries, transcoding and remuxing rights for each account.")}</p>
        </div>
        <Link to="/settings" className="btn btn-ghost btn-sm">
          <IconChevronLeft size={16} />{' '}{t("Back to settings")}</Link>
      </div>

      <section className="settings-card">
        {error && <div className="inline-error">{t(error)}</div>}
        {notice && <div className="inline-ok">{notice}</div>}
        {loading && <Spinner label={t("Loading users…")} />}
        {overview && (
          <>
            <div className="admin-block-head">
              <h3>{t("All accounts")}</h3>
              <span className="muted">{overview.users.length}{' '}{t("accounts")}</span>
            </div>
            <div className="user-admin-list">
              {overview.users.map((u) => (
                <UserAdminRow
                  key={u.id}
                  user={u}
                  libraries={overview.libraries}
                  isSelf={u.id === (session?.user.Id || '')}
                  onChanged={load}
                />
              ))}
            </div>
            <form className="add-user" onSubmit={submitUser}>
              <label className="field">
                <span>{t("Username")}</span>
                <input value={newUser} onChange={(e) => setNewUser(e.target.value)} />
              </label>
              <label className="field">
                <span>{t("Password")}</span>
                <input type="password" value={newPass} onChange={(e) => setNewPass(e.target.value)} />
              </label>
              <label className="check-line">
                <input type="checkbox" checked={newAdmin} onChange={(e) => setNewAdmin(e.target.checked)} />{t("Administrator")}</label>
              <button className="btn btn-ghost btn-sm" disabled={!newUser || !newPass}>{t("Create user")}</button>
            </form>
            <div className="path-line">{t("New accounts can access every library and may transcode and remux by default.")}</div>
          </>
        )}
      </section>
    </div>
  )
}

function UserAdminRow({
  user,
  libraries,
  isSelf,
  onChanged,
}: {
  user: AdminUser
  libraries: LibraryInfo[]
  isSelf: boolean
  onChanged: () => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [allLibs, setAllLibs] = useState(user.allowedLibraries.length === 0)
  const [libs, setLibs] = useState<string[]>(user.allowedLibraries)
  const [transcode, setTranscode] = useState(user.allowTranscode)
  const [remux, setRemux] = useState(user.allowRemux)
  const [admin, setAdmin] = useState(user.isAdmin)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const toggleLib = (id: string) => {
    setLibs((prev) => (prev.includes(id) ? prev.filter((v) => v !== id) : [...prev, id]))
  }

  const savePolicy = async () => {
    setError('')
    setNotice('')
    if (!allLibs && libs.length === 0) {
      setError(t("Select at least one library, or allow access to all libraries"))
      return
    }
    setBusy(true)
    try {
      await updateUser(user.id, {
        allowedLibraries: allLibs ? [] : libs,
        allowTranscode: transcode,
        allowRemux: remux,
        isAdmin: admin,
      })
      setNotice(t("Permissions saved"))
      await onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Save failed"))
    } finally {
      setBusy(false)
    }
  }

  const resetPassword = async () => {
    setError('')
    setNotice('')
    if (password.length < 4) {
      setError(t("Use at least 4 characters"))
      return
    }
    setBusy(true)
    try {
      await updateUser(user.id, { password })
      setPassword('')
      setNotice(t("Password reset"))
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Reset failed"))
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!window.confirm(t("Delete user \"{0}\"? Their watch history and tokens will also be removed.", { 0: user.name }))) return
    setError('')
    setBusy(true)
    try {
      await deleteUser(user.id)
      await onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Delete failed"))
      setBusy(false)
    }
  }

  const summary = `${allLibs ? t("All libraries") : t("{0} libraries", { 0: user.allowedLibraries.length })} · ${
    user.allowTranscode ? t("Allow transcoding") : t("Transcoding disabled")
  } · ${user.allowRemux ? t("Allow remuxing") : t("Remuxing disabled")}`

  return (
    <div className="user-admin">
      <div className="user-admin-head">
        <IconUser size={16} />
        <span className="user-admin-name">{user.name}</span>
        {user.isAdmin && <span className="tag">{t("Administrator")}</span>}
        {user.hasPassword === false && <span className="tag muted-tag">{t("No password")}</span>}
        <span className="user-admin-summary">{summary}</span>
        <button className="btn btn-ghost btn-sm" onClick={() => setOpen((v) => !v)}>
          {open ? t("Collapse") : t("Manage")}
        </button>
      </div>
      {open && (
        <div className="user-admin-body">
          {error && <div className="form-error">{t(error)}</div>}
          {notice && <div className="form-ok">{notice}</div>}

          <div>
            <div className="setting-label">{t("Visible libraries")}</div>
            {admin ? (
              <div className="setting-hint">{t("Administrators can always access every library.")}</div>
            ) : (
              <>
                <label className="check-line" style={{ height: 'auto', margin: '8px 0' }}>
                  <input
                    type="checkbox"
                    checked={allLibs}
                    onChange={(e) => {
                      setAllLibs(e.target.checked)
                      if (e.target.checked) setLibs([])
                      else setLibs(libraries.map((l) => l.id))
                    }}
                  />{t("Allow access to all libraries")}</label>
                {!allLibs && (
                  <div className="user-libs">
                    {libraries.map((l) => (
                      <label key={l.id} className="user-lib">
                        <input type="checkbox" checked={libs.includes(l.id)} onChange={() => toggleLib(l.id)} />
                        {l.name}
                      </label>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>

          <div className="user-admin-grid">
            <div className="setting-row" style={{ margin: 0 }}>
              <div>
                <div className="setting-label">{t("Allow transcoding")}</div>
                <div className="setting-hint">{t("Incompatible formats are encoded to H.264 + AAC.")}</div>
              </div>
              <Switch checked={transcode} onChange={setTranscode} />
            </div>
            <div className="setting-row" style={{ margin: 0 }}>
              <div>
                <div className="setting-label">{t("Allow remuxing")}</div>
                <div className="setting-hint">{t("Only repackages the container without re-encoding. Much cheaper.")}</div>
              </div>
              <Switch checked={remux} onChange={setRemux} />
            </div>
            <div className="setting-row" style={{ margin: 0 }}>
              <div>
                <div className="setting-label">{t("Administrator")}</div>
                <div className="setting-hint">{t("Can manage the server, libraries and other accounts.")}</div>
              </div>
              <Switch checked={admin} onChange={setAdmin} />
            </div>
          </div>

          <div className="user-admin-actions">
            <button className="btn btn-primary btn-sm" onClick={savePolicy} disabled={busy}>
              {busy ? t("Saving…") : t("Save permissions")}
            </button>
            {!isSelf && (
              <button className="btn btn-ghost btn-sm danger-text" onClick={remove} disabled={busy}>
                <IconTrash size={15} />{' '}{t("Delete account")}</button>
            )}
          </div>

          <div className="user-admin-pass">
            <label className="field">
              <span>{t("Reset password")}</span>
              <input
                type="password"
                value={password}
                placeholder={t("Leave blank to keep current")}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <button className="btn btn-ghost btn-sm" onClick={resetPassword} disabled={busy || !password}>{t("Reset password")}</button>
          </div>
          {isSelf && <div className="path-line">{t("This is the signed-in account. It cannot be deleted or demoted.")}</div>}
        </div>
      )}
    </div>
  )
}
