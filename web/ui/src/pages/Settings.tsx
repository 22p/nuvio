import { useEffect, useState, type CSSProperties, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  addLibrary,
  changePassword,
  getAdminOverview,
  getScanStatus,
  removeLibrary,
  saveServerSettings,
  triggerScan,
} from '../lib/api'
import { useAuth } from '../lib/auth'
import { usePrefs } from '../lib/prefs'
import type { AdminOverview } from '../lib/types'
import { Spinner } from '../components/Media'
import { Switch } from '../components/Controls'
import { languages, t, useI18n, type Lang } from '../lib/i18n'
import {
  IconInfo,
  IconRefresh,
  IconTrash,
} from '../components/Icons'

const BITRATES = [
  { value: 0, label: 'Auto' },
  { value: 4, label: '4 Mbps' },
  { value: 8, label: '8 Mbps' },
  { value: 12, label: '12 Mbps' },
  { value: 20, label: '20 Mbps' },
  { value: 40, label: '40 Mbps' },
]

const LANGS = [
  { value: 'auto', label: 'Auto' },
  { value: 'zh', label: 'Chinese' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: 'Japanese' },
  { value: 'ko', label: 'Korean' },
]

export function SettingsPage() {
  const { session, isAdmin, config, signOut, refreshConfig } = useAuth()
  const { prefs, update, reset } = usePrefs()
  const { lang, setLang } = useI18n()
  const location = useLocation()
  const navigate = useNavigate()

  useEffect(() => {
    if (location.hash === '#users') {
      navigate('/users', { replace: true })
      return
    }
    if (!location.hash) return
    const el = document.getElementById(location.hash.slice(1))
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [location.hash])

  return (
    <div className="page-pad settings-page">
      <div className="page-heading">
        <div>
          <div className="eyebrow">{t("Preferences")}</div>
          <h1>{t("Settings")}</h1>
          <p className="muted">{t("Playback and subtitle preferences are stored in this browser. Server settings apply to everyone.")}</p>
        </div>
      </div>

      <div className="settings-layout">
        <aside className="settings-nav">
          <a href="#language">{t("Language")}</a>
          <a href="#playback">{t("Play")}</a>
          <a href="#subtitles">{t("Subtitles")}</a>
          <a href="#account">{t("Account")}</a>
          {isAdmin && <a href="#server">{t("Server")}</a>}
          <a href="#about">{t("About")}</a>
        </aside>

        <div className="settings-content">
          <section id="language" className="settings-card">
            <header>
              <h2>{t("Interface language")}</h2>
              <p>{t("Choose the web interface language. Changes apply immediately.")}</p>
            </header>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Language")}</div>
                <div className="setting-hint">{t("Only affects the web interface, not media metadata.")}</div>
              </div>
              <Select value={lang} options={languages} onChange={(v) => setLang(v as Lang)} />
            </div>
          </section>

          <section id="playback" className="settings-card">
            <header>
              <h2>{t("Play")}</h2>
              <p>{t("Choose when to play the original file and when to transcode.")}</p>
            </header>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Bitrate limit")}</div>
                <div className="setting-hint">{t("Videos above the limit are transcoded to HLS (requires ffmpeg).")}</div>
              </div>
              <Segmented
                value={prefs.maxBitrateMbps}
                options={BITRATES.map((o) => ({ ...o, label: t(o.label) }))}
                onChange={(v) => update({ maxBitrateMbps: v })}
              />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Prefer direct play")}</div>
                <div className="setting-hint">{t("When off, always transcode. Useful on limited bandwidth.")}</div>
              </div>
              <Switch checked={prefs.directPlay} onChange={(v) => update({ directPlay: v })} />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Preferred audio language")}</div>
                <div className="setting-hint">{t("Automatically pick the matching audio track.")}</div>
              </div>
              <Select value={prefs.audioLang} options={LANGS.map((o) => ({ ...o, label: t(o.label) }))} onChange={(v) => update({ audioLang: v })} />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Auto-play next episode")}</div>
                <div className="setting-hint">{t("Play the next episode automatically when one ends.")}</div>
              </div>
              <Switch checked={prefs.autoNext} onChange={(v) => update({ autoNext: v })} />
            </div>
          </section>

          <section id="subtitles" className="settings-card">
            <header>
              <h2>{t("Subtitles")}</h2>
              <p>{t("Subtitles load separately as WebVTT; video is not re-encoded.")}</p>
            </header>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Display mode")}</div>
                <div className="setting-hint">{t("Auto: show subtitles when the audio language differs from your preference.")}</div>
              </div>
              <Segmented
                value={prefs.subtitleMode}
                options={[
                  { value: 'off', label: t("Close") },
                  { value: 'auto', label: t("Auto") },
                  { value: 'always', label: t("Always") },
                ]}
                onChange={(v) => update({ subtitleMode: v as 'off' | 'auto' | 'always' })}
              />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Preferred subtitle language")}</div>
                <div className="setting-hint">{t("Preferred when several subtitles are available.")}</div>
              </div>
              <Select value={prefs.subtitleLang} options={LANGS.map((o) => ({ ...o, label: t(o.label) }))} onChange={(v) => update({ subtitleLang: v })} />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Font size")}</div>
                <div className="setting-hint">{t("Relative to player height.")}</div>
              </div>
              <input
                className="range"
                type="range"
                min={0.7}
                max={1.8}
                step={0.05}
                value={prefs.cueScale}
                onChange={(e) => update({ cueScale: Number(e.target.value) })}
              />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Color")}</div>
                <div className="setting-hint">{t("Subtitle text color.")}</div>
              </div>
              <div className="swatches">
                {['#ffffff', '#ffe082', '#a5f3fc', '#bbf7d0', '#fca5a5'].map((color) => (
                  <button
                    key={color}
                    className={`swatch ${prefs.cueColor === color ? 'active' : ''}`}
                    style={{ background: color }}
                    onClick={() => update({ cueColor: color })}
                    aria-label={color}
                  />
                ))}
              </div>
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Background opacity")}</div>
                <div className="setting-hint">{t("Keeps subtitles readable on bright scenes.")}</div>
              </div>
              <input
                className="range"
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={prefs.cueBg}
                onChange={(e) => update({ cueBg: Number(e.target.value) })}
              />
            </div>
            <div className="setting-row">
              <div>
                <div className="setting-label">{t("Text outline")}</div>
                <div className="setting-hint">{t("Improves readability on busy backgrounds.")}</div>
              </div>
              <Switch checked={prefs.cueOutline} onChange={(v) => update({ cueOutline: v })} />
            </div>
            <div className="cue-preview-wrap">
              <div
                className="cue-preview"
                style={
                  {
                    '--cue-color': prefs.cueColor,
                    '--cue-bg': `rgba(0,0,0,${prefs.cueBg})`,
                    '--cue-scale': prefs.cueScale,
                    '--cue-shadow': prefs.cueOutline ? '0 2px 10px rgba(0,0,0,.95), 0 0 2px #000' : 'none',
                  } as CSSProperties
                }
              >
                <span className="cue-line">{t("Subtitle preview")}</span>
              </div>
              <button className="text-button" onClick={reset}>{t("Reset to defaults")}</button>
            </div>
          </section>

          <section id="account" className="settings-card">
            <header>
              <h2>{t("Account")}</h2>
              <p>{t("The web app and Jellyfin clients share the same account.")}</p>
            </header>
            <div className="account-row">
              <div className="avatar avatar-lg">{session?.user.Name.slice(0, 1).toUpperCase()}</div>
              <div>
                <div className="account-name">{session?.user.Name}</div>
                <div className="setting-hint">
                  {session?.serverName} · {isAdmin ? t("Administrator") : t("Standard user")}
                </div>
              </div>
              <button className="btn btn-ghost btn-sm" onClick={() => signOut()}>{t("Sign out")}</button>
            </div>
            <ChangePassword />
          </section>

          {isAdmin && (
            <AdminSection onConfigChange={refreshConfig} />
          )}

          <section id="about" className="settings-card">
            <header>
              <h2>{t("About")}</h2>
              <p>{t("Version and API information.")}</p>
            </header>
            <div className="about-grid">
              <div>
                <span>{t("Version")}</span>
                <strong>{config?.version || '1.0.0'}</strong>
              </div>
              <div>
                <span>{t("Jellyfin API")}</span>
                <strong>{config?.apiVersion || '12.4.0'}</strong>
              </div>
              <div>
                <span>{t("Service name")}</span>
                <strong>{config?.serverName}</strong>
              </div>
              <div>
                <span>{t("Transcoding")}</span>
                <strong>{config?.ffmpeg ? t("ffmpeg ready") : t("ffmpeg not installed")}</strong>
              </div>
            </div>
            <p className="about-note">
              This product uses the TMDB API but is not endorsed or certified by TMDB.
              <br />{t("This product uses the TMDb API but is not endorsed or certified by TMDb.")}</p>
          </section>
        </div>
      </div>
    </div>
  )
}


function Segmented<T extends string | number>({
  value,
  options,
  onChange,
}: {
  value: T
  options: { value: T; label: string }[]
  onChange: (v: T) => void
}) {
  return (
    <div className="segmented">
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          className={o.value === value ? 'active' : ''}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function Select({
  value,
  options,
  onChange,
}: {
  value: string
  options: { value: string; label: string }[]
  onChange: (v: string) => void
}) {
  return (
    <label className="select-wrap compact">
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  )
}

function ChangePassword() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setMsg('')
    setError('')
    if (next.length < 4) return setError(t("New password must be at least 4 characters"))
    if (next !== confirm) return setError(t("The entries do not match"))
    setBusy(true)
    try {
      await changePassword(current, next)
      setMsg(t("Password updated"))
      setCurrent('')
      setNext('')
      setConfirm('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Update failed"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="password-form" onSubmit={submit}>
      <div className="form-subhead">{t("Change password")}</div>
      <div className="form-grid-3">
        <label className="field">
          <span>{t("Current password")}</span>
          <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </label>
        <label className="field">
          <span>{t("New password")}</span>
          <input type="password" value={next} onChange={(e) => setNext(e.target.value)} />
        </label>
        <label className="field">
          <span>{t("Confirm new password")}</span>
          <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>
      </div>
      {error && <div className="form-error">{t(error)}</div>}
      {msg && <div className="form-ok">{msg}</div>}
      <button className="btn btn-ghost btn-sm" disabled={busy}>{t("Update password")}</button>
    </form>
  )
}

function AdminSection({ onConfigChange }: { onConfigChange: () => Promise<unknown> }) {
  const [overview, setOverview] = useState<AdminOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [serverName, setServerName] = useState('')
  const [transcode, setTranscode] = useState(true)
  const [remux, setRemux] = useState(true)
  const [hardwareAccel, setHardwareAccel] = useState('auto')
  const [maxBitrate, setMaxBitrate] = useState(20)
  const [preset, setPreset] = useState('veryfast')
  const [ffmpegPath, setFfmpegPath] = useState('')
  const [ffprobePath, setFfprobePath] = useState('')
  const [tmdbApiKey, setTmdbApiKey] = useState('')
  const [metadataLanguage, setMetadataLanguage] = useState('zh-CN')
  const [saving, setSaving] = useState(false)
  const [scanning, setScanning] = useState(false)

  const [libName, setLibName] = useState('')
  const [libType, setLibType] = useState('movies')
  const [libPaths, setLibPaths] = useState('')
  const [adding, setAdding] = useState(false)


  const load = async () => {
    try {
      const data = await getAdminOverview()
      setOverview(data)
      setServerName(data.serverName)
      setTranscode(data.settings.transcodeEnabled)
      setRemux(data.settings.remuxEnabled !== false)
      setHardwareAccel(data.settings.hardwareAccel || 'auto')
      setMaxBitrate(data.settings.maxBitrateMbps)
      setPreset(data.settings.transcodePreset || 'veryfast')
      setFfmpegPath(data.settings.ffmpegPath || '')
      setFfprobePath(data.settings.ffprobePath || '')
      setTmdbApiKey(data.settings.tmdbApiKey || '')
      setMetadataLanguage(data.settings.metadataLanguage || 'zh-CN')
      setScanning(data.scan.scanning)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Could not load server information"))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!scanning) return
    const timer = window.setInterval(async () => {
      try {
        const status = await getScanStatus()
        if (!status.scanning) {
          setScanning(false)
          window.clearInterval(timer)
          load()
        }
      } catch {
        /* keep polling */
      }
    }, 1500)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scanning])

  const save = async () => {
    setSaving(true)
    setError('')
    setNotice('')
    try {
      await saveServerSettings({
        serverName,
        transcodeEnabled: transcode,
        remuxEnabled: remux,
        hardwareAccel,
        maxBitrateMbps: maxBitrate,
        transcodePreset: preset,
        ffmpegPath,
        ffprobePath,
        tmdbApiKey,
        metadataLanguage,
      })
      setNotice(t("Server settings saved"))
      await onConfigChange()
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Save failed"))
    } finally {
      setSaving(false)
    }
  }

  const scan = async () => {
    setScanning(true)
    setNotice('')
    try {
      await triggerScan()
    } catch (err) {
      setScanning(false)
      setError(err instanceof Error ? err.message : t("Could not start the scan"))
    }
  }

  const remove = async (id: string) => {
    if (!window.confirm(t("Delete this library? Files on disk will not be removed."))) return
    try {
      await removeLibrary(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Delete failed"))
    }
  }

  const submitLibrary = async (e: FormEvent) => {
    e.preventDefault()
    setAdding(true)
    setError('')
    try {
      const paths = libPaths
        .split('\n')
        .map((p) => p.trim())
        .filter(Boolean)
      await addLibrary(libName.trim(), libType, paths)
      setLibName('')
      setLibPaths('')
      setScanning(true)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Could not add"))
    } finally {
      setAdding(false)
    }
  }


  if (loading) return <section className="settings-card"><Spinner label={t("Loading server information…")} /></section>
  if (!overview) {
    return (
      <section id="server" className="settings-card">
        <header><h2>{t("Server")}</h2></header>
        <div className="inline-error">{error || t("Could not read server information")}</div>
      </section>
    )
  }

  const totalItems = overview.counts.Total || 0

  return (
    <>
      <section id="server" className="settings-card">
        <header>
          <h2>{t("Server")}</h2>
          <p>{t("Libraries, transcoding and metadata. ")}{' '}{totalItems}{' '}{t("items.")}</p>
        </header>

        {error && <div className="inline-error">{t(error)}</div>}
        {overview.scan.lastError && <div className="hint-box">{t("Scan or metadata: ")}{overview.scan.lastError}</div>}

        <div className="server-stats">
          <div>
            <span>{t("Media libraries")}</span>
            <strong>{overview.libraries.length}</strong>
          </div>
          <div>
            <span>{t("Movies")}</span>
            <strong>{overview.counts.Movie || 0}</strong>
          </div>
          <div>
            <span>{t("Series")}</span>
            <strong>{overview.counts.Series || 0}</strong>
          </div>
          <div>
            <span>{t("Episode")}</span>
            <strong>{overview.counts.Episode || 0}</strong>
          </div>
        </div>

        <div className="admin-block">
          <div className="admin-block-head">
            <h3>{t("Media libraries")}</h3>
            <button className="btn btn-ghost btn-sm" onClick={scan} disabled={scanning}>
              <IconRefresh size={16} /> {scanning ? t("Scanning…") : t("Scan now")}
            </button>
          </div>
          <div className="library-list">
            {overview.libraries.map((lib) => (
              <div key={lib.id} className="admin-library">
                <div className="admin-library-main">
                  <div className="admin-library-name">
                    {lib.name}
                    {lib.demo && <span className="tag">{t("Demo")}</span>}
                    <span className="tag muted-tag">
                      {lib.type === 'movies' ? t("Movies") : lib.type === 'tvshows' ? t("Series") : t("Mixed")}
                    </span>
                  </div>
                  <div className="admin-library-path">
                    {lib.paths?.length ? lib.paths.join(' · ') : t("Built-in demo content")}
                  </div>
                </div>
                <div className="admin-library-count">{lib.itemCount}{' '}{t("Item")}</div>
                <button
                  className="icon-button danger"
                  onClick={() => remove(lib.id)}
                  aria-label={t("Delete library")}
                >
                  <IconTrash size={16} />
                </button>
              </div>
            ))}
          </div>

          <form className="add-library" onSubmit={submitLibrary}>
            <div className="form-grid-3">
              <label className="field">
                <span>{t("Name")}</span>
                <input value={libName} onChange={(e) => setLibName(e.target.value)} placeholder={t("e.g. Movies")} />
              </label>
              <label className="field">
                <span>{t("Type")}</span>
                <select value={libType} onChange={(e) => setLibType(e.target.value)}>
                  <option value="movies">{t("Movies")}</option>
                  <option value="tvshows">{t("Series")}</option>
                  <option value="mixed">{t("Mixed")}</option>
                </select>
              </label>
              <label className="field">
                <span>{t("Folders (one absolute path per line)")}</span>
                <textarea
                  rows={2}
                  value={libPaths}
                  onChange={(e) => setLibPaths(e.target.value)}
                  placeholder="/mnt/media/movies"
                />
              </label>
            </div>
            <button className="btn btn-primary btn-sm" disabled={adding}>
              {adding ? t("Adding…") : t("Add library")}
            </button>
          </form>
        </div>

        <div className="admin-block">
          <div className="admin-block-head">
            <h3>{t("Transcoding")}</h3>
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
              <span className={`status-dot ${overview.ffmpeg ? 'ok' : 'warn'}`}>
                {overview.ffmpeg
                  ? t("ffmpeg ready{0}", { 0: overview.encoder ? ` · ${overview.encoder}` : '' })
                  : t("ffmpeg not found")}
              </span>
              <span className={`status-dot ${overview.hardware ? 'ok' : 'warn'}`}>
                {overview.hardware === 'qsv'
                  ? t("QSV ready")
                  : overview.hardware
                    ? t("VAAPI ready")
                    : t("Hardware unavailable")}
              </span>
            </div>
          </div>
          {!overview.ffmpeg && (
            <p className="hint-box">{t("Without ffmpeg only direct play is possible. Formats the browser cannot play, such as MKV/HEVC, need transcoding. Install ffmpeg on the server (e.g.")}{ ' '}<code>apt install ffmpeg</code>{t(") and enter the path here.")}</p>
          )}
          <div className="setting-row">
            <div>
              <div className="setting-label">{t("Enable transcoding")}</div>
              <div className="setting-hint">{t("HLS with H.264 + AAC for clients that need it.")}</div>
            </div>
            <Switch checked={transcode} onChange={setTranscode} />
          </div>
          <div className="setting-row">
            <div>
              <div className="setting-label">{t("Enable remuxing")}</div>
              <div className="setting-hint">{t("When only the container is incompatible, package HLS with -c copy instead of re-encoding.")}</div>
            </div>
            <Switch checked={remux} onChange={setRemux} />
          </div>
          <div className="setting-row">
            <div>
              <div className="setting-label">{t("Hardware acceleration")}</div>
              <div className="setting-hint">{t("Uses VAAPI (Intel / AMD) or QSV (Intel) through /dev/dri. Falls back to software when unavailable.")}</div>
            </div>
            <Select
              value={hardwareAccel}
              options={[
                { value: 'auto', label: t("Auto") },
                { value: 'vaapi', label: 'VAAPI (Intel / AMD)' },
                { value: 'qsv', label: 'QSV (Intel)' },
                { value: 'none', label: t("Off") },
              ]}
              onChange={setHardwareAccel}
            />
          </div>
          <div className="form-grid-3">
            <label className="field">
              <span>{t("Default bitrate (Mbps)")}</span>
              <input
                type="number"
                min={1}
                max={200}
                value={maxBitrate}
                onChange={(e) => setMaxBitrate(Number(e.target.value))}
              />
            </label>
            <label className="field">
              <span>{t("Encoder preset")}</span>
              <select value={preset} onChange={(e) => setPreset(e.target.value)}>
                <option value="ultrafast">ultrafast</option>
                <option value="veryfast">veryfast</option>
                <option value="fast">fast</option>
                <option value="medium">medium</option>
                <option value="slow">slow</option>
              </select>
            </label>
            <label className="field">
              <span>{t("Server name")}</span>
              <input value={serverName} onChange={(e) => setServerName(e.target.value)} />
            </label>
            <label className="field">
              <span>{t("ffmpeg path")}</span>
              <input value={ffmpegPath} onChange={(e) => setFfmpegPath(e.target.value)} placeholder={t("Leave blank to auto-detect")} />
            </label>
            <label className="field">
              <span>{t("ffprobe path")}</span>
              <input value={ffprobePath} onChange={(e) => setFfprobePath(e.target.value)} placeholder={t("Leave blank to auto-detect")} />
            </label>
            <div className="field field-align">
              <button className="btn btn-primary btn-sm" onClick={save} disabled={saving}>
                {saving ? t("Saving…") : t("Save settings")}
              </button>
            </div>
          </div>
          {notice && <div className="inline-ok">{notice}</div>}
          <div className="path-line">{t("Data directory: ")}<code>{overview.dataPath}</code>
            {overview.dbPath && (
              <>
                {' · '}{t("Database: ")}<code>{overview.dbPath}</code>
              </>
            )}
          </div>
        </div>

        <div className="admin-block">
          <div className="admin-block-head">
            <h3>{t("Metadata")}</h3>
            <span className={`status-dot ${overview.tmdb ? 'ok' : 'warn'}`}>
              {overview.tmdb ? t("TMDb configured") : t("TMDb not configured")}
            </span>
          </div>
          <p className="setting-hint" style={{ marginBottom: 14 }}>{t("With a TMDb API key, rescanning fills in posters, overviews, ratings and genres. Local artwork takes priority.")}</p>
          <div className="form-grid-3">
            <label className="field">
              <span>TMDb API Key</span>
              <input
                value={tmdbApiKey}
                onChange={(e) => setTmdbApiKey(e.target.value)}
                placeholder={t("Enter your v3 API key")}
                autoComplete="off"
              />
            </label>
            <label className="field">
              <span>{t("Metadata language")}</span>
              <select value={metadataLanguage} onChange={(e) => setMetadataLanguage(e.target.value)}>
                <option value="zh-CN">{t("Simplified Chinese")}</option>
                <option value="zh-TW">{t("Traditional Chinese")}</option>
                <option value="en-US">English</option>
                <option value="ja-JP">{t("Japanese")}</option>
                <option value="ko-KR">한국어</option>
              </select>
            </label>
            <div className="field field-align">
              <button className="btn btn-primary btn-sm" onClick={save} disabled={saving}>
                {saving ? t("Saving…") : t("Save metadata settings")}
              </button>
            </div>
          </div>
          <div className="path-line">{t("Saving triggers a rescan. Artwork is cached locally.")}</div>
        </div>

        <div className="admin-foot">
          <IconInfo size={15} />{t("API version")}{' '}{overview.apiVersion}{' '}{t("· Data directory")}{' '}<code>{overview.dataPath}</code>
        </div>
      </section>

    </>
  )
}

