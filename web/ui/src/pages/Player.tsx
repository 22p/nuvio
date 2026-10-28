import { t } from '../lib/i18n'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import Hls from 'hls.js'
import {
  getEpisodes,
  getItem,
  getPlaybackInfo,
  getToken,
  setPlayed,
} from '../lib/api'
import { useAuth } from '../lib/auth'
import { usePrefs } from '../lib/prefs'
import type { JFItem, MediaSource, MediaStream } from '../lib/types'
import {
  episodeCode,
  formatTime,
  secondsToTicks,
  ticksToSeconds,
} from '../lib/format'
import { Spinner } from '../components/Media'
import {
  IconAudio,
  IconBack,
  IconCheck,
  IconClose,
  IconExitFullscreen,
  IconFullscreen,
  IconMute,
  IconNext,
  IconPause,
  IconPip,
  IconPlay,
  IconSettings,
  IconSubtitles,
  IconVolume,
} from '../components/Icons'

type Menu = 'subs' | 'audio' | 'quality' | 'speed' | 'settings' | null
type Mode = 'direct' | 'hls'

interface PlaybackStats {
  width: number
  height: number
  dropped: number
  total: number
  buffer: number
  level: string
  bandwidth: number
  rate: number
}

interface AudioTrackList {
  length: number
  [index: number]: { enabled: boolean }
}

interface Cue {
  start: number
  end: number
  text: string
  line?: number
  lineAlign?: 'start' | 'center' | 'end'
  position?: number
  align?: 'start' | 'center' | 'end'
}

function parseVTT(input: string): Cue[] {
  const normalized = input.replace(/\r\n/g, '\n').replace(/^\uFEFF/, '')
  const blocks = normalized.split(/\n{2,}/)
  const cues: Cue[] = []
  for (const block of blocks) {
    const lines = block.split('\n')
    const timingIndex = lines.findIndex((l) => l.includes('-->'))
    if (timingIndex < 0) continue
    const [from, toRaw = ''] = lines[timingIndex].split('-->')
    const toParts = toRaw.trim().split(/\s+/)
    const to = toParts[0] || ''
    const settings: Record<string, string> = {}
    for (const part of toParts.slice(1)) {
      const comma = part.indexOf(':')
      if (comma > 0) settings[part.slice(0, comma)] = part.slice(comma + 1)
    }
    const parse = (v: string) => {
      const m = v.trim().match(/(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})/)
      if (!m) return 0
      const h = Number(m[1] || 0)
      const min = Number(m[2])
      const sec = Number(m[3])
      const ms = Number(m[4].padEnd(3, '0'))
      return h * 3600 + min * 60 + sec + ms / 1000
    }
    const text = lines
      .slice(timingIndex + 1)
      .join('\n')
      .replace(/<[^>]+>/g, '')
      .replace(/&amp;/g, '&')
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
      .replace(/&nbsp;/g, ' ')
      .trim()
    if (!text) continue
    const cue: Cue = { start: parse(from), end: parse(to), text }
    const line = Number.parseFloat(settings.line)
    const position = Number.parseFloat(settings.position)
    if (Number.isFinite(line)) cue.line = line
    if (Number.isFinite(position)) cue.position = position
    if (settings.align === 'start' || settings.align === 'center' || settings.align === 'end') {
      cue.align = settings.align
    }
    if (
      settings['line-align'] === 'start' ||
      settings['line-align'] === 'center' ||
      settings['line-align'] === 'end'
    ) {
      cue.lineAlign = settings['line-align']
    }
    cues.push(cue)
  }
  return cues
}

function cueTextStyle(prefs: ReturnType<typeof usePrefs>['prefs']): CSSProperties {
  return {
    color: prefs.cueColor,
    background: `rgba(0,0,0,${prefs.cueBg})`,
    textShadow: prefs.cueOutline
      ? '0 1px 2px #000, 0 2px 12px rgba(0,0,0,.95), 0 0 2px #000'
      : 'none',
    fontSize: `clamp(15px, calc(1.15vw * ${prefs.cueScale} + 9px), 38px)`,
  }
}

// Cue settings come from the ASS converter, which maps \an / \pos and the
// style margins onto line / position / align.
function cuePositionStyle(cue: Cue): CSSProperties {
  const x = cue.position ?? 50
  const y = cue.line ?? 88
  const align = cue.align ?? 'center'
  const lineAlign = cue.lineAlign ?? 'center'
  const tx = align === 'start' ? '0' : align === 'end' ? '-100%' : '-50%'
  const ty = lineAlign === 'start' ? '0' : lineAlign === 'end' ? '-100%' : '-50%'
  return {
    left: `${x}%`,
    top: `${y}%`,
    bottom: 'auto',
    transform: `translate(${tx}, ${ty})`,
    textAlign: align === 'start' ? 'left' : align === 'end' ? 'right' : 'center',
  }
}

const QUALITIES = [
  { value: 0, label: 'Auto' },
  { value: 4, label: '4 Mbps' },
  { value: 8, label: '8 Mbps' },
  { value: 12, label: '12 Mbps' },
  { value: 20, label: '20 Mbps' },
  { value: 40, label: '40 Mbps' },
]

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2]

function langMatch(code?: string, pref?: string): boolean {
  if (!code || !pref) return false
  const c = code.toLowerCase()
  const p = pref.toLowerCase()
  return c === p || c.startsWith(p + '-') || p.startsWith(c + '-')
}

function pickAudio(streams: MediaStream[], pref: string): number {
  const audio = streams.filter((s) => s.Type === 'Audio')
  if (!audio.length) return 1
  if (pref !== 'auto') {
    const found = audio.find((s) => langMatch(s.Language, pref))
    if (found) return found.Index
  }
  return audio[0].Index
}

function chooseSubtitle(
  subs: MediaStream[],
  audioLang: string | undefined,
  mode: 'off' | 'auto' | 'always',
  lang: string,
): number | null {
  if (mode === 'off' || subs.length === 0) return null
  if (lang !== 'auto') {
    const found = subs.find((s) => langMatch(s.Language, lang))
    if (found) return found.Index
    if (mode === 'always') return subs[0].Index
    return null
  }
  if (mode === 'always') return subs[0].Index
  const forced = subs.find((s) => s.IsForced)
  if (forced) return forced.Index
  const foreign = subs.find(
    (s) => s.Language && s.Language !== 'und' && (!audioLang || !langMatch(s.Language, audioLang)),
  )
  return foreign ? foreign.Index : null
}

function absolutePosition(video: HTMLVideoElement | null, mode: Mode, offset: number): number {
  if (!video) return 0
  return mode === 'hls' ? offset + video.currentTime : video.currentTime
}

function formatMbps(bps?: number): string {
  return bps && bps > 0 ? `${(bps / 1_000_000).toFixed(1)} Mbps` : '—'
}

function setNativeAudio(video: HTMLVideoElement | null, index: number): boolean {
  const tracks = (video as (HTMLVideoElement & { audioTracks?: AudioTrackList }) | null)?.audioTracks
  if (!tracks || tracks.length < 2 || !tracks[index - 1]) return false
  for (let i = 0; i < tracks.length; i++) tracks[i].enabled = i === index - 1
  return true
}

export function PlayerPage() {
  const { id = '' } = useParams()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { session } = useAuth()
  const { prefs, update } = usePrefs()

  const [item, setItem] = useState<JFItem | null>(null)
  const [source, setSource] = useState<MediaSource | null>(null)
  const [sessionId, setSessionId] = useState('')
  const [mode, setMode] = useState<Mode>('direct')
  const [offset, setOffset] = useState(0)
  const [reload, setReload] = useState(0)
  const [audioIndex, setAudioIndex] = useState(1)
  const [subChoice, setSubChoice] = useState<'auto' | 'off' | number>('auto')
  const [rate, setRate] = useState(() => {
    const stored = Number(localStorage.getItem('nuvio.rate') || 1)
    return RATES.includes(stored) ? stored : 1
  })
  const [menu, setMenu] = useState<Menu>(null)
  const [playing, setPlaying] = useState(false)
  const [current, setCurrent] = useState(0)
  const [duration, setDuration] = useState(0)
  const [buffered, setBuffered] = useState(0)
  const [cues, setCues] = useState<Cue[]>([])
  const [volume, setVolume] = useState(() => Number(localStorage.getItem('nuvio.volume') || 0.9))
  const [muted, setMuted] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const [loadingMeta, setLoadingMeta] = useState(true)
  const [playbackLoading, setPlaybackLoading] = useState(false)
  const [buffering, setBuffering] = useState(false)
  const [needsGesture, setNeedsGesture] = useState(false)
  const [error, setError] = useState('')
  const [ended, setEnded] = useState(false)
  const [nextEpisode, setNextEpisode] = useState<JFItem | null>(null)
  const [countdown, setCountdown] = useState(0)
  const [controls, setControls] = useState(true)
  const [dragging, setDragging] = useState(false)
  const [seekPreview, setSeekPreview] = useState(0)
  const [stats, setStats] = useState<PlaybackStats>({
    width: 0,
    height: 0,
    dropped: 0,
    total: 0,
    buffer: 0,
    level: '',
    bandwidth: 0,
    rate: 1,
  })

  const videoRef = useRef<HTMLVideoElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const hlsRef = useRef<Hls | null>(null)
  const hideTimer = useRef(0)
  const startedRef = useRef(false)
  const stateRef = useRef({
    item: null as JFItem | null,
    source: null as MediaSource | null,
    sessionId: '',
    mode: 'direct' as Mode,
    offset: 0,
    audioIndex: 1,
    duration: 0,
    dragging: false,
  })
  const requestRef = useRef({ start: 0, audio: 1, bitrate: 0, disableDirectPlay: false })

  useEffect(() => {
    stateRef.current = { item, source, sessionId, mode, offset, audioIndex, duration, dragging }
  }, [item, source, sessionId, mode, offset, audioIndex, duration, dragging])

  const streams = useMemo(() => source?.MediaStreams || item?.MediaSources?.[0]?.MediaStreams || [], [source, item])
  const subtitleStreams = useMemo(() => streams.filter((s) => s.Type === 'Subtitle'), [streams])
  const audioStreams = useMemo(() => streams.filter((s) => s.Type === 'Audio'), [streams])
  const videoStream = useMemo(() => streams.find((s) => s.Type === 'Video'), [streams])
  const activeAudio = audioStreams.find((a) => a.Index === audioIndex)
  const autoSubtitle = useMemo(
    () => chooseSubtitle(subtitleStreams, activeAudio?.Language, prefs.subtitleMode, prefs.subtitleLang),
    [subtitleStreams, activeAudio, prefs.subtitleMode, prefs.subtitleLang],
  )
  const activeSubtitle: number | null =
    subChoice === 'auto' ? autoSubtitle : subChoice === 'off' ? null : subChoice
  const activeSubtitleStream = subtitleStreams.find((s) => s.Index === activeSubtitle)
  const videoDetails = [
    videoStream?.Codec ? videoStream.Codec.toUpperCase() : '',
    videoStream?.Profile || '',
    videoStream?.RealFrameRate
      ? `${videoStream.RealFrameRate.toFixed(2)} fps`
      : videoStream?.AverageFrameRate
        ? `${videoStream.AverageFrameRate.toFixed(2)} fps`
        : '',
    videoStream?.BitRate ? formatMbps(videoStream.BitRate) : '',
  ]
    .filter(Boolean)
    .join(' · ')

  useEffect(() => {
    const url = activeSubtitleStream?.DeliveryUrl
    if (!url) {
      setCues([])
      return
    }
    // The player draws cues itself, so ask the server for WebVTT even when the
    // track is delivered as native ASS for libass based clients.
    const fetchUrl = url.replace(/Stream\.(ass|ssa)(\?|$)/i, 'Stream.vtt$2')
    let alive = true
    const controller = new AbortController()
    fetch(fetchUrl, { signal: controller.signal })
      .then((res) => (res.ok ? res.text() : ''))
      .then((text) => {
        if (alive) setCues(parseVTT(text))
      })
      .catch(() => {
        if (alive) setCues([])
      })
    return () => {
      alive = false
      controller.abort()
    }
  }, [activeSubtitleStream?.DeliveryUrl])

  // Fetch item metadata and prepare the first playback request.
  useEffect(() => {
    if (!session) return
    let alive = true
    setLoadingMeta(true)
    getItem(session.user.Id, id)
      .then((it) => {
        if (!alive) return
        setItem(it)
        const srcMeta = it.MediaSources?.[0]
        const resumeTicks = it.UserData?.PlaybackPositionTicks || 0
        const tParam = Number(searchParams.get('t') || 0)
        const total = srcMeta?.RunTimeTicks || it.RunTimeTicks || 0
        let start = tParam > 0 ? tParam * 10_000_000 : resumeTicks
        if (total && start > total * 0.96) start = 0
        const audio = pickAudio(srcMeta?.MediaStreams || [], prefs.audioLang)
        setAudioIndex(audio)
        setSubChoice('auto')
        setEnded(false)
        setNextEpisode(null)
        startedRef.current = false
        requestRef.current = { start, audio, bitrate: (prefs.maxBitrateMbps || 0) * 1_000_000, disableDirectPlay: false }
        startedRef.current = false
        setReload((n) => n + 1)
      })
      .catch((err) => {
        if (alive) setError(err instanceof Error ? err.message : t("Could not load the video"))
      })
      .finally(() => {
        if (alive) setLoadingMeta(false)
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, session, searchParams])

  // Fetch playback info whenever a reload is requested (quality / audio / seek).
  useEffect(() => {
    if (!item || !session || reload === 0) return
    let alive = true
    const req = requestRef.current
    setPlaybackLoading(true)
    setError('')
    getPlaybackInfo(item.Id, session.user.Id, {
      startTicks: req.start,
      audioIndex: req.audio > 1 ? req.audio : undefined,
      maxBitrate: req.bitrate,
      disableDirectPlay: req.disableDirectPlay,
    })
      .then((info) => {
        if (!alive) return
        const src = info.MediaSources?.[0]
        if (!src) {
          setError(t("The server returned no playable media source"))
          return
        }
        startedRef.current = false
        setSource(src)
        setSessionId(info.PlaySessionId)
        const startSec = req.start / 10_000_000
        setOffset(startSec)
        setDuration(
          ((src.RunTimeTicks || item.MediaSources?.[0]?.RunTimeTicks || 0) / 10_000_000) || 0,
        )
        setCurrent(startSec)
        setEnded(false)
      })
      .catch((err) => {
        if (alive) {
          setError(err instanceof Error ? err.message : t("Could not get a playback URL"))
          setPlaybackLoading(false)
        }
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reload, item?.Id])

  // Attach the selected media source to the <video> element.
  useEffect(() => {
    const video = videoRef.current
    if (!video || !source) return
    let disposed = false
    setEnded(false)
    setNeedsGesture(false)
    setBuffering(true)
    setPlaybackLoading(true)

    if (hlsRef.current) {
      hlsRef.current.destroy()
      hlsRef.current = null
    }
    video.pause()
    video.removeAttribute('src')
    video.load()

    const useHls =
      !!source.TranscodingUrl &&
      !(prefs.directPlay && source.SupportsDirectPlay && source.DirectStreamUrl)
    setMode(useHls ? 'hls' : 'direct')

    const onReady = () => {
      if (disposed) return
      setPlaybackLoading(false)
      setBuffering(false)
      const p = video.play()
      if (p) p.catch(() => setNeedsGesture(true))
    }

    if (useHls && source.TranscodingUrl && Hls.isSupported()) {
      const hls = new Hls({
        enableWorker: true,
        lowLatencyMode: false,
        maxBufferLength: 24,
        backBufferLength: 30,
      })
      hlsRef.current = hls
      hls.loadSource(source.TranscodingUrl)
      hls.attachMedia(video)
      hls.on(Hls.Events.MANIFEST_PARSED, onReady)
      hls.on(Hls.Events.ERROR, (_evt, data) => {
        if (!data.fatal) return
        setPlaybackLoading(false)
        setBuffering(false)
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
          setError(t("Could not connect to the transcoded stream. ffmpeg may be missing or the video cannot be converted."))
        } else {
          setError(t("Playback of the transcoded stream failed. Retry or change the bitrate."))
        }
      })
    } else if (useHls && source.TranscodingUrl) {
      video.src = source.TranscodingUrl
      video.addEventListener('loadedmetadata', onReady, { once: true })
    } else if (source.DirectStreamUrl) {
      video.src = source.DirectStreamUrl
      const start = requestRef.current.start / 10_000_000
      const onMeta = () => {
        if (disposed) return
        if (start > 0 && Number.isFinite(video.duration)) {
          video.currentTime = Math.min(start, Math.max(0, video.duration - 1))
        }
        setPlaybackLoading(false)
        setBuffering(false)
        const p = video.play()
        if (p) p.catch(() => setNeedsGesture(true))
      }
      video.addEventListener('loadedmetadata', onMeta, { once: true })
    } else {
      setError(t("This item has no media source"))
      setPlaybackLoading(false)
    }

    return () => {
      disposed = true
      if (hlsRef.current) {
        hlsRef.current.destroy()
        hlsRef.current = null
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source])

  // Media element events.
  useEffect(() => {
    const video = videoRef.current
    if (!video) return

    const updateBuffered = () => {
      if (!video.buffered.length) return setBuffered(0)
      setBuffered(video.buffered.end(video.buffered.length - 1))
    }
    const onTime = () => {
      const s = stateRef.current
      if (!s.dragging) {
        setCurrent(absolutePosition(video, s.mode, s.offset))
      }
      if (s.mode === 'direct' && Number.isFinite(video.duration) && video.duration > 0) {
        setDuration(video.duration)
      }
      updateBuffered()
    }
    const onPlay = () => {
      setPlaying(true)
      setEnded(false)
      setNeedsGesture(false)
    }
    const onPause = () => {
      setPlaying(false)
      if (startedRef.current) reportProgress(true)
    }
    const onWaiting = () => setBuffering(true)
    const onPlaying = () => {
      setBuffering(false)
      setPlaybackLoading(false)
    }
    const onVolume = () => {
      setMuted(video.muted)
      setVolume(video.volume)
    }
    const onEnded = () => {
      setEnded(true)
      setPlaying(false)
      setControls(true)
      const s = stateRef.current
      if (s.item && s.sessionId) {
        sendReport('/Sessions/Playing/Progress', s, true)
      }
      if (s.item && session) {
        setPlayed(session.user.Id, s.item.Id, true).catch(() => {})
      }
    }
    const onLoaded = () => {
      updateBuffered()
      if (stateRef.current.mode === 'direct' && Number.isFinite(video.duration)) {
        setDuration(video.duration)
      }
    }
    const onError = () => {
      setPlaybackLoading(false)
      setBuffering(false)
      setError(t("This format cannot play in your browser. Lower the bitrate in Settings or install ffmpeg on the server."))
    }

    video.addEventListener('timeupdate', onTime)
    video.addEventListener('play', onPlay)
    video.addEventListener('pause', onPause)
    video.addEventListener('waiting', onWaiting)
    video.addEventListener('playing', onPlaying)
    video.addEventListener('volumechange', onVolume)
    video.addEventListener('ended', onEnded)
    video.addEventListener('loadedmetadata', onLoaded)
    video.addEventListener('progress', updateBuffered)
    video.addEventListener('error', onError)
    return () => {
      video.removeEventListener('timeupdate', onTime)
      video.removeEventListener('play', onPlay)
      video.removeEventListener('pause', onPause)
      video.removeEventListener('waiting', onWaiting)
      video.removeEventListener('playing', onPlaying)
      video.removeEventListener('volumechange', onVolume)
      video.removeEventListener('ended', onEnded)
      video.removeEventListener('loadedmetadata', onLoaded)
      video.removeEventListener('progress', updateBuffered)
      video.removeEventListener('error', onError)
    }
    // Re-bind when the <video> element mounts (it is absent while metadata loads)
    // or when a new media source is attached.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session, item?.Id, source])

  // Volume persistence.
  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    video.volume = volume
    video.muted = muted
    localStorage.setItem('nuvio.volume', String(volume))
  }, [volume, muted, item?.Id])

  // Playback rate persistence and re-application after source switches.
  useEffect(() => {
    const video = videoRef.current
    if (video) video.playbackRate = rate
    localStorage.setItem('nuvio.rate', String(rate))
  }, [rate, source])

  // Live playback statistics for the info panel.
  useEffect(() => {
    if (menu !== 'settings') return
    const update = () => {
      const video = videoRef.current
      const hls = hlsRef.current
      let level = ''
      let bandwidth = 0
      if (hls) {
        const current = hls.currentLevel >= 0 ? hls.levels[hls.currentLevel] : undefined
        if (current?.height) level = `${current.height}p`
        bandwidth = hls.bandwidthEstimate || current?.bitrate || 0
      }
      const quality = video?.getVideoPlaybackQuality?.()
      const bufferedEnd = video?.buffered.length ? video.buffered.end(video.buffered.length - 1) : 0
      setStats({
        width: video?.videoWidth || 0,
        height: video?.videoHeight || 0,
        dropped: quality?.droppedVideoFrames || 0,
        total: quality?.totalVideoFrames || 0,
        buffer: Math.max(0, bufferedEnd - (video?.currentTime || 0)),
        level,
        bandwidth,
        rate: video?.playbackRate || 1,
      })
    }
    update()
    const timer = window.setInterval(update, 1000)
    return () => window.clearInterval(timer)
  }, [menu, source, mode])

  const wake = useCallback(() => {
    setControls(true)
    window.clearTimeout(hideTimer.current)
    if (playing && !menu) {
      hideTimer.current = window.setTimeout(() => setControls(false), 3200)
    }
  }, [playing, menu])

  useEffect(() => {
    wake()
    return () => window.clearTimeout(hideTimer.current)
  }, [wake])

  useEffect(() => {
    const onFs = () => setFullscreen(Boolean(document.fullscreenElement))
    document.addEventListener('fullscreenchange', onFs)
    return () => document.removeEventListener('fullscreenchange', onFs)
  }, [])

  // Progress reporting.
  const sendReport = (path: string, s: typeof stateRef.current, isPaused: boolean) => {
    const video = videoRef.current
    if (!video || !s.item || !s.sessionId) return
    const pos = absolutePosition(video, s.mode, s.offset)
    fetch(path, {
      method: 'POST',
      keepalive: true,
      headers: {
        'Content-Type': 'application/json',
        'X-Emby-Token': getToken(),
      },
      body: JSON.stringify({
        ItemId: s.item.Id,
        MediaSourceId: s.source?.Id,
        PlaySessionId: s.sessionId,
        PositionTicks: secondsToTicks(pos),
        IsPaused: isPaused,
        IsMuted: video.muted,
        VolumeLevel: Math.round(video.volume * 100),
        PlayMethod: s.mode === 'hls' ? 'Transcode' : 'DirectPlay',
        CanSeek: true,
      }),
    }).catch(() => {})
  }

  const reportProgress = (isPaused = false) => sendReport('/Sessions/Playing/Progress', stateRef.current, isPaused)

  useEffect(() => {
    if (!playing) return
    const timer = window.setInterval(() => {
      if (!document.hidden) reportProgress(false)
    }, 10000)
    return () => window.clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playing])

  useEffect(() => {
    if (!playing) return
    if (startedRef.current) return
    startedRef.current = true
    sendReport('/Sessions/Playing', stateRef.current, false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playing, sessionId])

  useEffect(() => {
    return () => {
      if (startedRef.current) {
        sendReport('/Sessions/Playing/Stopped', stateRef.current, true)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Next episode lookup.
  useEffect(() => {
    if (!item || !session) return
    if (item.Type !== 'Episode' || !item.SeriesId) {
      setNextEpisode(null)
      return
    }
    let alive = true
    getEpisodes(session.user.Id, item.SeriesId)
      .then((res) => {
        if (!alive) return
        const list = res.Items || []
        const idx = list.findIndex((e) => e.Id === item.Id)
        setNextEpisode(idx >= 0 ? list[idx + 1] || null : null)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [item, session])

  // Auto advance.
  useEffect(() => {
    if (!ended || !nextEpisode || !prefs.autoNext) return
    setCountdown(8)
    let left = 8
    const timer = window.setInterval(() => {
      left -= 1
      setCountdown(Math.max(0, left))
      if (left <= 0) {
        window.clearInterval(timer)
        navigate(`/watch/${nextEpisode.Id}`)
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [ended, nextEpisode, prefs.autoNext, navigate])

  // Keyboard shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null
      if (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) return
      const video = videoRef.current
      if (!video) return
      const s = stateRef.current
      switch (e.key.toLowerCase()) {
        case ' ':
        case 'k':
          e.preventDefault()
          video.paused ? video.play().catch(() => {}) : video.pause()
          break
        case 'arrowright':
          e.preventDefault()
          seek(absolutePosition(video, s.mode, s.offset) + 10)
          break
        case 'arrowleft':
          e.preventDefault()
          seek(absolutePosition(video, s.mode, s.offset) - 10)
          break
        case 'arrowup':
          e.preventDefault()
          setVolume((v) => Math.min(1, v + 0.05))
          break
        case 'arrowdown':
          e.preventDefault()
          setVolume((v) => Math.max(0, v - 0.05))
          break
        case 'm':
          setMuted((m) => !m)
          break
        case 'f':
          toggleFullscreen()
          break
        case 'c':
          cycleSubtitles()
          break
        case 'i':
          e.preventDefault()
          setMenu((m) => (m === 'settings' ? null : 'settings'))
          break
        case 'escape':
          if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
          setMenu(null)
          break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [subtitleStreams, activeSubtitle, duration, mode, offset])

  const currentPosition = (): number => absolutePosition(videoRef.current, stateRef.current.mode, stateRef.current.offset)

  const seek = (target: number) => {
    const s = stateRef.current
    if (!s.item) return
    const total = s.duration || 0
    const clamped = Math.min(Math.max(0, target), Math.max(0, total - 0.5))
    if (s.mode === 'direct') {
      const video = videoRef.current
      if (video) {
        video.currentTime = clamped
        setCurrent(clamped)
        if (startedRef.current) reportProgress(true)
      }
      return
    }
    reloadAt(clamped)
  }

  const reloadAt = (seconds: number, audio = stateRef.current.audioIndex) => {
    videoRef.current?.pause()
    if (startedRef.current) sendReport('/Sessions/Playing/Stopped', stateRef.current, true)
    startedRef.current = false
    requestRef.current = {
      start: secondsToTicks(Math.max(0, seconds)),
      audio,
      bitrate: (prefs.maxBitrateMbps || 0) * 1_000_000,
      disableDirectPlay: false,
    }
    setOffset(seconds)
    setCurrent(seconds)
    setPlaybackLoading(true)
    setEnded(false)
    setMenu(null)
    setReload((n) => n + 1)
  }

  const changeQuality = (mbps: number) => {
    const s = stateRef.current
    const pos = currentPosition()
    videoRef.current?.pause()
    if (startedRef.current) sendReport('/Sessions/Playing/Stopped', stateRef.current, true)
    startedRef.current = false
    requestRef.current = {
      start: secondsToTicks(pos),
      audio: s.audioIndex,
      bitrate: mbps * 1_000_000,
      disableDirectPlay: false,
    }
    update({ maxBitrateMbps: mbps })
    setPlaybackLoading(true)
    setMenu(null)
    setReload((n) => n + 1)
  }

  const changeRate = (value: number) => {
    setRate(value)
    const video = videoRef.current
    if (video) video.playbackRate = value
    setMenu(null)
    wake()
  }

  const changeAudio = (index: number) => {
    if (index === audioIndex) {
      setMenu(null)
      return
    }
    // Safari/WebKit can switch audio tracks inside a direct-playing file.
    if (mode === 'direct' && setNativeAudio(videoRef.current, index)) {
      setAudioIndex(index)
      setMenu(null)
      return
    }
    const pos = currentPosition()
    videoRef.current?.pause()
    setAudioIndex(index)
    requestRef.current = {
      start: secondsToTicks(pos),
      audio: index,
      bitrate: (prefs.maxBitrateMbps || 0) * 1_000_000,
      disableDirectPlay: true,
    }
    if (startedRef.current) sendReport('/Sessions/Playing/Stopped', stateRef.current, true)
    startedRef.current = false
    setOffset(pos)
    setCurrent(pos)
    setPlaybackLoading(true)
    setMenu(null)
    setReload((n) => n + 1)
  }

  const togglePlay = () => {
    const video = videoRef.current
    if (!video) return
    if (video.paused) video.play().catch(() => setNeedsGesture(true))
    else video.pause()
    wake()
  }

  const toggleMute = () => setMuted((m) => !m)

  const toggleFullscreen = () => {
    const el = containerRef.current
    if (!el) return
    if (!document.fullscreenElement) el.requestFullscreen?.().catch(() => {})
    else document.exitFullscreen?.().catch(() => {})
  }

  const togglePip = async () => {
    const video = videoRef.current
    if (!video) return
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture()
      else await video.requestPictureInPicture()
    } catch {
      /* unsupported */
    }
  }

  const cycleSubtitles = () => {
    if (!subtitleStreams.length) return
    if (activeSubtitle == null) setSubChoice(subtitleStreams[0].Index)
    else setSubChoice('off')
  }

  // Seek bar interaction.
  const seekRatio = (clientX: number, el: HTMLElement): number => {
    const rect = el.getBoundingClientRect()
    return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width))
  }

  const onTimelineDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    setDragging(true)
    const ratio = seekRatio(e.clientX, el)
    setSeekPreview(ratio * duration)
  }

  const onTimelineMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragging) return
    const ratio = seekRatio(e.clientX, e.currentTarget)
    setSeekPreview(ratio * duration)
  }

  const onTimelineUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragging) return
    const ratio = seekRatio(e.clientX, e.currentTarget)
    setDragging(false)
    seek(ratio * duration)
  }

  const displayCurrent = dragging ? seekPreview : current
  const activeCues =
    !ended && cues.length
      ? cues.filter((c) => displayCurrent >= c.start - 0.02 && displayCurrent <= c.end)
      : []
  const plainCues = activeCues.filter((c) => c.line === undefined && c.position === undefined)
  const positionedCues = activeCues.filter((c) => c.line !== undefined || c.position !== undefined)
  const playedPct = duration > 0 ? Math.min(100, (displayCurrent / duration) * 100) : 0
  const bufferedPct =
    duration > 0
      ? Math.min(100, ((buffered + (mode === 'hls' ? offset : 0)) / duration) * 100)
      : 0

  if (loadingMeta) {
    return (
      <div className="player is-loading">
        <Spinner label={t("Preparing playback…")} />
      </div>
    )
  }

  if (!item) {
    return (
      <div className="player is-loading">
        <div className="player-error">
          <h2>{t("Cannot play")}</h2>
          <p>{error || t("Video not found.")}</p>
          <button className="btn btn-ghost" onClick={() => navigate(-1)}>{t("Back")}</button>
        </div>
      </div>
    )
  }

  const title = item.Type === 'Episode' ? item.Name : item.Name
  const subtitle = item.Type === 'Episode'
    ? `${item.SeriesName} · ${episodeCode(item.ParentIndexNumber, item.IndexNumber)}`
    : [item.ProductionYear, item.OfficialRating].filter(Boolean).join(' · ')

  return (
    <div
      ref={containerRef}
      className={`player ${controls ? 'controls-on' : 'controls-off'} ${playing ? 'is-playing' : ''}`}
      onMouseMove={wake}
      onTouchStart={wake}
    >

      <video
        ref={videoRef}
        className="player-video"
        playsInline
        onClick={(e) => {
          if (e.detail === 1) togglePlay()
        }}
        onDoubleClick={toggleFullscreen}
      />

      {!error && plainCues.length > 0 && (
        <div className="subtitle-layer" style={cueTextStyle(prefs)}>
          {plainCues.map((c) => c.text).join('\n')}
        </div>
      )}
      {!error &&
        positionedCues.map((cue, i) => (
          <div key={i} className="subtitle-layer" style={{ ...cueTextStyle(prefs), ...cuePositionStyle(cue) }}>
            {cue.text}
          </div>
        ))}

      <div className="player-shade top" />
      <div className="player-shade bottom" />

      <header className="player-topbar">
        <button className="glass-button" onClick={() => navigate(`/item/${item.Id}`, { state: { fromPlayer: true } })} aria-label={t("Back")}>
          <IconBack size={20} />
        </button>
        <div className="player-heading">
          <div className="player-title">{title}</div>
          <div className="player-sub">{subtitle}</div>
        </div>
        <div className="player-head-actions">
          <span className="play-method">
            {mode === 'hls' ? (source?.TranscodingUrl?.includes('Remux=1') ? t("Remux HLS") : t("Transcode HLS")) : t("Direct")}
          </span>
        </div>
      </header>

      {(playbackLoading || buffering) && !error && (
        <div className="player-center">
          <Spinner />
        </div>
      )}

      {needsGesture && !playing && !playbackLoading && (
        <button className="big-play" onClick={togglePlay} aria-label={t("Play")}>
          <IconPlay size={34} />
        </button>
      )}

      {!playing && !playbackLoading && !needsGesture && !ended && !error && (
        <button className="big-play soft" onClick={togglePlay} aria-label={t("Play")}>
          <IconPlay size={34} />
        </button>
      )}

      {error && (
        <div className="player-center">
          <div className="player-error">
            <h2>{t("Playback problem")}</h2>
            <p>{t(error)}</p>
            <div className="player-error-actions">
              <button className="btn btn-ghost" onClick={() => navigate(`/item/${item.Id}`, { state: { fromPlayer: true } })}>{t("Back to details")}</button>
              <button
                className="btn btn-primary"
                onClick={() => {
                  setError('')
                  reloadAt(currentPosition())
                }}
              >{t("Retry")}</button>
            </div>
            <p className="player-hint">{t("MKV/HEVC and similar formats need ffmpeg on the server.")}</p>
          </div>
        </div>
      )}

      {ended && !error && (
        <div className="ended-overlay">
          <div className="ended-card">
            <div className="eyebrow">{t("Playback ended")}</div>
            <h2>{nextEpisode ? nextEpisode.Name : item.Name}</h2>
            {nextEpisode && (
              <p>
                {nextEpisode.SeriesName} ·{' '}
                {episodeCode(nextEpisode.ParentIndexNumber, nextEpisode.IndexNumber)}
                {countdown > 0 ? t(" · playing in {0}s", { 0: countdown }) : ''}
              </p>
            )}
            <div className="ended-actions">
              <button
                className="btn btn-ghost"
                onClick={() => {
                  setEnded(false)
                  if (mode === 'hls') reloadAt(0)
                  else {
                    const v = videoRef.current
                    if (v) {
                      v.currentTime = 0
                      v.play().catch(() => {})
                    }
                  }
                }}
              >{t("Play again")}</button>
              {nextEpisode && (
                <button className="btn btn-primary" onClick={() => navigate(`/watch/${nextEpisode.Id}`)}>
                  <IconNext size={17} />{' '}{t("Next episode")}</button>
              )}
            </div>
          </div>
        </div>
      )}

      <div className="player-controls">
        <div className="timeline-wrap">
          <div
            className="timeline"
            onPointerDown={onTimelineDown}
            onPointerMove={onTimelineMove}
            onPointerUp={onTimelineUp}
            onPointerCancel={() => setDragging(false)}
          >
            <div className="timeline-track">
              <div className="timeline-buffer" style={{ width: `${bufferedPct}%` }} />
              <div className="timeline-played" style={{ width: `${playedPct}%` }} />
              <div className="timeline-thumb" style={{ left: `${playedPct}%` }} />
            </div>
          </div>
          <div className="time-row">
            <span>{formatTime(displayCurrent)}</span>
            <span>{duration ? formatTime(duration) : '--:--'}</span>
          </div>
        </div>

        <div className="controls-row">
          <div className="controls-left">
            <button className="control-button" onClick={togglePlay} aria-label={playing ? t("Pause") : t("Play")}>
              {playing ? <IconPause size={22} /> : <IconPlay size={22} />}
            </button>
            {nextEpisode && (
              <button
                className="control-button hide-sm"
                onClick={() => navigate(`/watch/${nextEpisode.Id}`)}
                aria-label={t("Next episode")}
              >
                <IconNext size={21} />
              </button>
            )}
            <div className="volume-group">
              <button className="control-button" onClick={toggleMute} aria-label={t("Mute")}>
                {muted || volume === 0 ? <IconMute size={21} /> : <IconVolume size={21} />}
              </button>
              <input
                className="volume-range"
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={muted ? 0 : volume}
                onChange={(e) => {
                  const v = Number(e.target.value)
                  setVolume(v)
                  setMuted(v === 0)
                }}
                aria-label={t("Volume")}
              />
            </div>
            <span className="time-inline hide-sm">
              {formatTime(displayCurrent)} / {duration ? formatTime(duration) : '--:--'}
            </span>
          </div>

          <div className="controls-right">
            <button
              className={`control-button ${activeSubtitle != null ? 'is-active' : ''}`}
              onClick={() => setMenu(menu === 'subs' ? null : 'subs')}
              aria-label={t("Subtitles")}
            >
              <IconSubtitles size={21} />
            </button>
            {audioStreams.length > 0 && (
              <button
                className={`control-button ${menu === 'audio' ? 'is-active' : ''}`}
                onClick={() => setMenu(menu === 'audio' ? null : 'audio')}
                aria-label={t("Audio track")}
              >
                <IconAudio size={21} />
              </button>
            )}
            <button
              className={`control-button ${menu === 'quality' ? 'is-active' : ''}`}
              onClick={() => setMenu(menu === 'quality' ? null : 'quality')}
              aria-label={t("Quality")}
            >
              <span className="quality-label">{prefs.maxBitrateMbps ? `${prefs.maxBitrateMbps}M` : t("Auto")}</span>
            </button>
            <button
              className={`control-button ${menu === 'speed' ? 'is-active' : ''}`}
              onClick={() => setMenu(menu === 'speed' ? null : 'speed')}
              aria-label={t("Playback rate")}
            >
              <span className="quality-label">{rate}×</span>
            </button>
            <button
              className={`control-button ${menu === 'settings' ? 'is-active' : ''}`}
              onClick={() => setMenu(menu === 'settings' ? null : 'settings')}
              aria-label={t("Playback settings")}
            >
              <IconSettings size={21} />
            </button>
            <button className="control-button hide-sm" onClick={togglePip} aria-label={t("Picture in picture")}>
              <IconPip size={21} />
            </button>
            <button className="control-button" onClick={toggleFullscreen} aria-label={t("Fullscreen")}>
              {fullscreen ? <IconExitFullscreen size={21} /> : <IconFullscreen size={21} />}
            </button>
          </div>
        </div>
      </div>

      {menu === 'subs' && (
        <div className="player-menu subs-menu">
          <div className="menu-head">
            <span>{t("Subtitles")}</span>
            <button className="menu-close" onClick={() => setMenu(null)} aria-label={t("Close")}>
              <IconClose size={16} />
            </button>
          </div>
          <button
            className={`menu-option ${activeSubtitle == null ? 'active' : ''}`}
            onClick={() => {
              setSubChoice('off')
              setMenu(null)
            }}
          >
            <span>{t("Close")}</span>
            {activeSubtitle == null && <IconCheck size={16} />}
          </button>
          {subtitleStreams.map((s) => (
            <button
              key={s.Index}
              className={`menu-option ${activeSubtitle === s.Index ? 'active' : ''}`}
              onClick={() => {
                setSubChoice(s.Index)
                setMenu(null)
              }}
            >
              <span>
                {s.DisplayTitle || s.Language}
                <em>
                  {[
                    s.Language && s.Language.toLowerCase() !== 'und' ? s.Language.toUpperCase() : '',
                    s.IsForced ? t('forced') : '',
                    s.Codec ? s.Codec.toUpperCase() : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </em>
              </span>
              {activeSubtitle === s.Index && <IconCheck size={16} />}
            </button>
          ))}
          {!subtitleStreams.length && <div className="menu-empty">{t("This video has no subtitle track")}</div>}
        </div>
      )}

      {menu === 'audio' && (
        <div className="player-menu audio-menu">
          <div className="menu-head">
            <span>{t("Audio track")}</span>
            <button className="menu-close" onClick={() => setMenu(null)} aria-label={t("Close")}>
              <IconClose size={16} />
            </button>
          </div>
          {audioStreams.map((s) => (
            <button
              key={s.Index}
              className={`menu-option ${audioIndex === s.Index ? 'active' : ''}`}
              onClick={() => changeAudio(s.Index)}
            >
              <span>
                {s.DisplayTitle || s.Language}
                <em>
                  {[
                    s.Codec?.toUpperCase(),
                    s.Channels ? `${s.Channels}ch` : '',
                    s.IsDefault ? t("Default") : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </em>
              </span>
              {audioIndex === s.Index && <IconCheck size={16} />}
            </button>
          ))}
          {audioStreams.length === 1 && (
            <div className="menu-empty">{t("Only one audio track in this file")}</div>
          )}
        </div>
      )}

      {menu === 'quality' && (
        <div className="player-menu quality-menu">
          <div className="menu-head">
            <span>{t("Quality")}</span>
            <button className="menu-close" onClick={() => setMenu(null)} aria-label={t("Close")}>
              <IconClose size={16} />
            </button>
          </div>
          {QUALITIES.map((q) => (
            <button
              key={q.value}
              className={`menu-option ${prefs.maxBitrateMbps === q.value ? 'active' : ''}`}
              onClick={() => changeQuality(q.value)}
            >
              <span>
                {t(q.label)}
                {q.value === 0 && <em>{t("Direct play at source quality")}</em>}
              </span>
              {prefs.maxBitrateMbps === q.value && <IconCheck size={16} />}
            </button>
          ))}
        </div>
      )}

      {menu === 'speed' && (
        <div className="player-menu speed-menu">
          <div className="menu-head">
            <span>{t("Playback rate")}</span>
            <button className="menu-close" onClick={() => setMenu(null)} aria-label={t("Close")}>
              <IconClose size={16} />
            </button>
          </div>
          {RATES.map((value) => (
            <button
              key={value}
              className={`menu-option ${rate === value ? 'active' : ''}`}
              onClick={() => changeRate(value)}
            >
              <span>{value}×</span>
              {rate === value && <IconCheck size={16} />}
            </button>
          ))}
        </div>
      )}

      {menu === 'settings' && (
        <div className="player-menu info-menu">
          <div className="menu-head">
            <span>{t("Playback info")}</span>
            <button className="menu-close" onClick={() => setMenu(null)} aria-label={t("Close")}>
              <IconClose size={16} />
            </button>
          </div>
          <dl>
            <div>
              <dt>{t("Mode")}</dt>
              <dd>
                {mode === 'hls'
                  ? source?.TranscodingUrl?.includes('Remux=1')
                    ? t("Remux playback (HLS)")
                    : t("Transcode playback (HLS)")
                  : t("Direct play")}
              </dd>
            </div>
            <div>
              <dt>{t("Container")}</dt>
              <dd>{source?.Container?.toUpperCase() || '—'}</dd>
            </div>
            <div>
              <dt>{t("Resolution")}</dt>
              <dd>{videoStream?.Width && videoStream?.Height ? `${videoStream.Width} × ${videoStream.Height}` : '—'}</dd>
            </div>
            <div>
              <dt>{t("Output")}</dt>
              <dd>{stats.width && stats.height ? `${stats.width} × ${stats.height}` : '—'}</dd>
            </div>
            <div>
              <dt>{t("Video")}</dt>
              <dd>{videoDetails || '—'}</dd>
            </div>
            <div>
              <dt>{t("Audio")}</dt>
              <dd>
                {activeAudio
                  ? `${activeAudio.DisplayTitle || activeAudio.Language || ''}${
                      activeAudio.Channels ? ` · ${activeAudio.Channels}ch` : ''
                    }`
                  : '—'}
              </dd>
            </div>
            <div>
              <dt>{t("Subtitles")}</dt>
              <dd>{activeSubtitleStream ? activeSubtitleStream.DisplayTitle || activeSubtitleStream.Language : t("Off")}</dd>
            </div>
            <div>
              <dt>{t("Duration")}</dt>
              <dd>{duration ? formatTime(duration) : '—'}</dd>
            </div>
            <div>
              <dt>{t("Position")}</dt>
              <dd>
                {formatTime(current)} / {duration ? formatTime(duration) : '--:--'}
              </dd>
            </div>
            <div>
              <dt>{t("Buffer")}</dt>
              <dd>{stats.buffer > 0 ? `${stats.buffer.toFixed(1)} s` : '—'}</dd>
            </div>
            <div>
              <dt>{t("Dropped frames")}</dt>
              <dd>{stats.total > 0 ? `${stats.dropped} / ${stats.total}` : '—'}</dd>
            </div>
            <div>
              <dt>{t("Bandwidth")}</dt>
              <dd>{formatMbps(stats.bandwidth || source?.Bitrate)}</dd>
            </div>
            {mode === 'hls' && (
              <div>
                <dt>{t("Level")}</dt>
                <dd>{stats.level || t("Auto")}</dd>
              </div>
            )}
            <div>
              <dt>{t("Bitrate limit")}</dt>
              <dd>{prefs.maxBitrateMbps ? `${prefs.maxBitrateMbps} Mbps` : t("Auto")}</dd>
            </div>
            <div>
              <dt>{t("Playback rate")}</dt>
              <dd>{stats.rate.toFixed(2)}×</dd>
            </div>
            <div>
              <dt>{t("Session")}</dt>
              <dd>{sessionId ? sessionId.slice(0, 8) : '—'}</dd>
            </div>
          </dl>
          <button className="menu-option" onClick={() => navigate(`/item/${item.Id}`, { state: { fromPlayer: true } })}>
            <span>{t("View details / episodes")}</span>
          </button>
        </div>
      )}

      <button className="player-back-floating" onClick={() => navigate(`/item/${item.Id}`, { state: { fromPlayer: true } })} aria-label={t("Back to details")}>
        <IconClose size={18} />
      </button>
    </div>
  )
}
