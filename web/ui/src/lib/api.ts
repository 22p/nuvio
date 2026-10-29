import type {
  AdminOverview,
  AdminUser,
  AppConfig,
  AuthResult,
  DeviceSession,
  JFItem,
  PlaybackInfo,
  QueryResult,
  ScanStatus,
  UserData,
} from './types'

const TOKEN_KEY = 'nuvio.token'
const USER_KEY = 'nuvio.user'
const DEVICE_KEY = 'nuvio.device'

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string | null) {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

export function deviceId(): string {
  let id = localStorage.getItem(DEVICE_KEY)
  if (!id) {
    id = Math.random().toString(16).slice(2) + Math.random().toString(16).slice(2)
    localStorage.setItem(DEVICE_KEY, id)
  }
  return id
}

// deviceLabel describes this browser to the server, e.g. "Linux · Chrome".
// It is what the device manager shows for this sign-in.
export function deviceLabel(): string {
  const ua = navigator.userAgent
  let os = ''
  if (/Android/i.test(ua)) os = 'Android'
  else if (/iPhone|iPad|iPod/i.test(ua)) os = 'iOS'
  else if (/Windows/i.test(ua)) os = 'Windows'
  else if (/CrOS/i.test(ua)) os = 'ChromeOS'
  else if (/Mac OS X|Macintosh/i.test(ua)) os = 'macOS'
  else if (/Linux/i.test(ua)) os = 'Linux'

  let browser = ''
  if (/Edg\//.test(ua)) browser = 'Edge'
  else if (/OPR\//.test(ua)) browser = 'Opera'
  else if (/Firefox\//.test(ua)) browser = 'Firefox'
  else if (/CriOS\//.test(ua)) browser = 'Chrome'
  else if (/Chrome\//.test(ua)) browser = 'Chrome'
  else if (/Safari\//.test(ua)) browser = 'Safari'

  return [os, browser].filter(Boolean).join(' · ') || 'Browser'
}

function authHeader(): string {
  return `MediaBrowser Client="Nuvio Web", Device="${deviceLabel()}", DeviceId="${deviceId()}", Version="1.0.0"`
}

export async function api<T>(path: string, init: RequestInit = {}, token = getToken()): Promise<T> {
  const headers = new Headers(init.headers)
  if (token) {
    headers.set('X-Emby-Token', token)
    // Jellyfin clients identify themselves on every call; the server uses it
    // to keep the device list up to date.
    if (!headers.has('Authorization')) headers.set('Authorization', authHeader())
  }
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers })
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = await res.json()
      message = body.error || body.Error || body.message || message
    } catch {
      /* not json */
    }
    throw new ApiError(message || 'Request failed', res.status)
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export const getConfig = () => api<AppConfig>('/api/app/config', {}, '')

export async function login(username: string, password: string): Promise<AuthResult> {
  return api<AuthResult>(
    '/Users/AuthenticateByName',
    {
      method: 'POST',
      headers: { 'X-Emby-Authorization': authHeader() },
      body: JSON.stringify({ Username: username, Pw: password }),
    },
    '',
  )
}

export interface SetupResult {
  accessToken: string
  userId: string
  userName: string
  serverId: string
  serverName: string
}

export const setupServer = (name: string, password: string) =>
  api<SetupResult>('/api/setup', { method: 'POST', body: JSON.stringify({ name, password }) }, '')

export const logoutServer = () => api<void>('/api/app/logout', { method: 'POST' })

export const getMe = () => api<import('./types').JFUser>('/Users/Me')

export const getViews = (userId: string) =>
  api<QueryResult<JFItem>>(`/UserViews?userId=${encodeURIComponent(userId)}`)

export const getResume = (userId: string, limit = 14) =>
  api<QueryResult<JFItem>>(`/UserItems/Resume?userId=${encodeURIComponent(userId)}&limit=${limit}`)

export const getLatest = (userId: string, parentId: string, limit = 18) =>
  api<JFItem[]>(
    `/Items/Latest?userId=${encodeURIComponent(userId)}&parentId=${parentId}&limit=${limit}`,
  )

export const getNextUp = (userId: string, limit = 14, seriesId?: string) =>
  api<QueryResult<JFItem>>(
    `/Shows/NextUp?userId=${userId}&limit=${limit}${seriesId ? `&seriesId=${seriesId}` : ''}`,
  )

export function getItems(
  userId: string,
  params: Record<string, string | number | boolean | undefined>,
): Promise<QueryResult<JFItem>> {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, String(v))
  }
  q.set('userId', userId)
  return api<QueryResult<JFItem>>(`/Items?${q.toString()}`)
}

export const getItem = (userId: string, itemId: string) =>
  api<JFItem>(`/Items/${itemId}?userId=${encodeURIComponent(userId)}`)

export const getSeasons = (userId: string, seriesId: string) =>
  api<QueryResult<JFItem>>(`/Shows/${seriesId}/Seasons?userId=${userId}`)

export const getEpisodes = (userId: string, seriesId: string, seasonId?: string) =>
  api<QueryResult<JFItem>>(
    `/Shows/${seriesId}/Episodes?userId=${userId}${seasonId ? `&seasonId=${seasonId}` : ''}`,
  )

export const getSimilar = (userId: string, itemId: string) =>
  api<QueryResult<JFItem>>(`/Items/${itemId}/Similar?userId=${userId}`)

export function searchItems(userId: string, term: string): Promise<QueryResult<JFItem>> {
  const q = new URLSearchParams({
    searchTerm: term,
    recursive: 'true',
    includeItemTypes: 'Movie,Series,Episode',
    limit: '60',
    fields: 'Overview',
  })
  q.set('userId', userId)
  return api<QueryResult<JFItem>>(`/Items?${q.toString()}`)
}

export const setPlayed = (userId: string, itemId: string, played: boolean) =>
  api<UserData>(`/UserPlayedItems/${itemId}?userId=${encodeURIComponent(userId)}`, {
    method: played ? 'POST' : 'DELETE',
  })

export const setFavorite = (userId: string, itemId: string, fav: boolean) =>
  api<UserData>(`/UserFavoriteItems/${itemId}?userId=${encodeURIComponent(userId)}`, {
    method: fav ? 'POST' : 'DELETE',
  })

export interface PlaybackOptions {
  startTicks?: number
  audioIndex?: number
  subtitleIndex?: number
  maxBitrate?: number
  disableDirectPlay?: boolean
}

export function getPlaybackInfo(itemId: string, userId: string, opts: PlaybackOptions = {}) {
  const maxBitrate = opts.maxBitrate && opts.maxBitrate > 0 ? opts.maxBitrate : 120_000_000
  const body = {
    UserId: userId,
    StartTimeTicks: opts.startTicks || 0,
    AudioStreamIndex: opts.audioIndex && opts.audioIndex > 0 ? opts.audioIndex : undefined,
    SubtitleStreamIndex: opts.subtitleIndex,
    MaxStreamingBitrate: maxBitrate,
    EnableDirectPlay: !opts.disableDirectPlay,
    EnableDirectStream: true,
    EnableTranscoding: true,
    DeviceProfile: {
      MaxStreamingBitrate: maxBitrate,
      DirectPlayProfiles: [
        {
          Container: 'mp4,m4v,webm',
          Type: 'Video',
          VideoCodec: 'h264,vp9,av1',
          AudioCodec: 'aac,mp3,opus,vorbis,flac',
        },
      ],
      TranscodingProfiles: [
        {
          Container: 'ts',
          Type: 'Video',
          VideoCodec: 'h264',
          AudioCodec: 'aac',
          Protocol: 'hls',
          Context: 'Streaming',
          MaxAudioChannels: '2',
        },
      ],
      SubtitleProfiles: [{ Format: 'vtt', Method: 'External' }],
    },
  }
  return api<PlaybackInfo>(`/Items/${itemId}/PlaybackInfo`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export interface PlaybackReport {
  ItemId: string
  MediaSourceId?: string
  PlaySessionId?: string
  PositionTicks: number
  IsPaused: boolean
  IsMuted?: boolean
  PlayMethod?: string
  VolumeLevel?: number
}

export function reportStart(report: PlaybackReport) {
  return api<void>('/Sessions/Playing', { method: 'POST', body: JSON.stringify(report) }, getToken())
}

export function reportProgress(report: PlaybackReport) {
  return api<void>(
    '/Sessions/Playing/Progress',
    { method: 'POST', body: JSON.stringify(report) },
    getToken(),
  )
}

export function reportStopped(report: PlaybackReport) {
  return api<void>(
    '/Sessions/Playing/Stopped',
    { method: 'POST', body: JSON.stringify(report) },
    getToken(),
  )
}

export function imageUrl(
  id: string,
  kind: 'Primary' | 'Backdrop' | 'Thumb' | 'Logo' = 'Primary',
  width = 500,
  tag?: string,
) {
  // Images are readable without a token, so the URL carries only the image
  // version: a cached poster must not survive a metadata refresh.
  const params = new URLSearchParams({ maxWidth: String(width) })
  if (tag) params.set('tag', tag)
  return `/Items/${id}/Images/${kind}?${params.toString()}`
}

export const getScanStatus = () => api<ScanStatus>('/api/app/scan')
export const getSessions = () => api<DeviceSession[]>('/api/app/sessions')
export const revokeSession = (id: string) =>
  api<void>(`/api/app/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const getAdminOverview = () => api<AdminOverview>('/api/admin/overview')
export const triggerScan = () => api<{ scanning: boolean }>('/api/admin/scan', { method: 'POST' })

export const addLibrary = (name: string, type: string, paths: string[]) =>
  api<{ id: string }>('/api/admin/libraries', {
    method: 'POST',
    body: JSON.stringify({ name, type, paths }),
  })

export const removeLibrary = (id: string) =>
  api<void>(`/api/admin/libraries/${id}`, { method: 'DELETE' })

export const saveServerSettings = (settings: Record<string, unknown>) =>
  api<Record<string, unknown>>('/api/admin/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  })

export const changePassword = (current: string, next: string) =>
  api<void>('/api/app/password', {
    method: 'POST',
    body: JSON.stringify({ current, new: next }),
  })

export const createUser = (name: string, password: string, admin: boolean) =>
  api<AdminUser>('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({ name, password, admin }),
  })

export const updateUser = (
  id: string,
  payload: {
    password?: string
    isAdmin?: boolean
    allowedLibraries?: string[]
    allowTranscode?: boolean
    allowRemux?: boolean
  },
) => api<AdminUser>(`/api/admin/users/${id}`, { method: 'PUT', body: JSON.stringify(payload) })

export const deleteUser = (id: string) =>
  api<void>(`/api/admin/users/${id}`, { method: 'DELETE' })
