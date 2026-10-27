import { getLang, t, type Lang } from './i18n'

export const TICKS = 10_000_000

export function ticksToSeconds(ticks?: number | null): number {
  return Math.max(0, (ticks || 0) / TICKS)
}

export function secondsToTicks(sec: number): number {
  return Math.max(0, Math.floor(sec * TICKS))
}

export function formatRuntime(ticks?: number | null, compact = false): string {
  const total = Math.round(ticksToSeconds(ticks) / 60)
  if (!total) return ''
  const h = Math.floor(total / 60)
  const m = total % 60
  if (compact) {
    if (h === 0) return `${m}m`
    return m ? `${h}h ${m}m` : `${h}h`
  }
  if (h === 0) return t("{m} min", { m })
  return m ? t("{h}h {m}m", { h, m }) : t("{h}h", { h })
}

export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0
  const s = Math.floor(seconds % 60)
  const m = Math.floor((seconds / 60) % 60)
  const h = Math.floor(seconds / 3600)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

export function formatRating(rating?: number): string {
  return rating ? rating.toFixed(1) : ''
}

export function episodeCode(season?: number | null, episode?: number | null): string {
  if (season === undefined || season === null || episode === undefined || episode === null) return ''
  return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
}

export function playProgress(item: {
  UserData?: { PlayedPercentage?: number; PlaybackPositionTicks?: number }
  RunTimeTicks?: number
}): number {
  if (item.UserData?.PlayedPercentage) {
    return Math.min(100, Math.max(0, item.UserData.PlayedPercentage))
  }
  return progressPercent(item.UserData?.PlaybackPositionTicks, item.RunTimeTicks)
}

export function progressPercent(position?: number, runtime?: number): number {
  if (!position || !runtime) return 0
  return Math.min(100, Math.max(0, (position / runtime) * 100))
}

export function mediaYear(item: { ProductionYear?: number; PremiereDate?: string }): string {
  if (item.ProductionYear) return String(item.ProductionYear)
  if (item.PremiereDate) return item.PremiereDate.slice(0, 4)
  return ''
}

export function seasonLabel(name?: string, number?: number | null): string {
  if (!name) return number ? t("Season {n}", { n: number }) : t("Unknown season")
  if (/^specials?$/i.test(name)) return t("Specials")
  const m = name.match(/^season\s*(\d+)$/i)
  if (m) return t("Season {n}", { n: Number(m[1]) })
  return name
}

export function initials(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return 'N'
  return trimmed.slice(0, 1).toUpperCase()
}

export function formatDate(value?: string): string {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const locales: Record<Lang, string> = { en: 'en-US', zh: 'zh-CN', 'zh-TW': 'zh-TW', ja: 'ja-JP', ko: 'ko-KR' }
  const locale = locales[getLang()]
  return d.toLocaleDateString(locale, { year: 'numeric', month: 'long', day: 'numeric' })
}

// relativeTime renders a compact "last active" label for device sessions.
export function relativeTime(value?: string): string {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const minutes = Math.floor(Math.max(0, Date.now() - d.getTime()) / 60_000)
  if (minutes < 1) return t("just now")
  if (minutes < 60) return t("{0} min ago", { 0: minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t("{0} hr ago", { 0: hours })
  const days = Math.floor(hours / 24)
  if (days < 7) return t("{0} d ago", { 0: days })
  return formatDate(value)
}

export function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}

export function prettyContainer(container?: string): string {
  if (!container) return ''
  const map: Record<string, string> = {
    mp4: 'MP4',
    mkv: 'MKV',
    webm: 'WebM',
    avi: 'AVI',
    ts: 'TS',
    mov: 'MOV',
  }
  return map[container.toLowerCase()] || container.toUpperCase()
}
