import { t } from '../lib/i18n'
import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import {
  getEpisodes,
  getItem,
  getNextUp,
  getSeasons,
  getSimilar,
  imageUrl,
  setFavorite,
  setPlayed,
} from '../lib/api'
import { useAuth } from '../lib/auth'
import type { JFItem } from '../lib/types'
import {
  episodeCode,
  formatRating,
  formatRuntime,
  initials,
  playProgress,
  prettyContainer,
  seasonLabel,
} from '../lib/format'
import { EmptyState, MediaRow, Spinner } from '../components/Media'
import {
  IconBack,
  IconCheck,
  IconInfo,
  IconPlay,
  IconPlus,
  IconStar,
} from '../components/Icons'

export function DetailPage() {
  const { id = '' } = useParams()
  const { session } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [item, setItem] = useState<JFItem | null>(null)
  const [seasons, setSeasons] = useState<JFItem[]>([])
  const [allEpisodes, setAllEpisodes] = useState<JFItem[]>([])
  const [seasonId, setSeasonId] = useState('')
  const [similar, setSimilar] = useState<JFItem[]>([])
  const [nextTarget, setNextTarget] = useState<JFItem | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let alive = true
    setLoading(true)
    setItem(null)
    setSeasons([])
    setAllEpisodes([])
    setSimilar([])
    setNextTarget(null)
    const userId = session?.user.Id
    if (!userId) return

    ;(async () => {
      try {
        const it = await getItem(userId, id)
        if (!alive) return
        setItem(it)
        if (it.Type === 'Series') {
          const [seasonRes, episodeRes, nextRes] = await Promise.all([
            getSeasons(userId, it.Id),
            getEpisodes(userId, it.Id),
            getNextUp(userId, 1, it.Id).catch(() => null),
          ])
          if (!alive) return
          setSeasons(seasonRes.Items || [])
          const eps = (episodeRes.Items || []).slice().sort((a, b) => {
            const sa = a.ParentIndexNumber || 0
            const sb = b.ParentIndexNumber || 0
            if (sa !== sb) return sa - sb
            return (a.IndexNumber || 0) - (b.IndexNumber || 0)
          })
          setAllEpisodes(eps)
          const resume = eps.find((e) => (e.UserData?.PlaybackPositionTicks || 0) > 0)
          const target = nextRes?.Items?.[0] || resume || eps[0] || null
          setNextTarget(target)
          const wantedSeason = target?.SeasonId || seasonRes.Items?.[0]?.Id || ''
          setSeasonId(wantedSeason)
        } else if (it.Type === 'Season') {
          const episodeRes = await getEpisodes(userId, it.SeriesId || '', it.Id)
          if (!alive) return
          const eps = episodeRes.Items || []
          setAllEpisodes(eps)
          setNextTarget(eps.find((e) => !e.UserData?.Played) || eps[0] || null)
        } else {
          setNextTarget(it)
        }

        if (it.Type !== 'Season' && it.Type !== 'Episode') {
          getSimilar(userId, it.Id)
            .then((res) => {
              if (alive) setSimilar(res.Items || [])
            })
            .catch(() => {})
        }
        setError('')
      } catch (err) {
        if (alive) setError(err instanceof Error ? err.message : t("Could not load the item"))
      } finally {
        if (alive) setLoading(false)
      }
    })()

    return () => {
      alive = false
    }
  }, [id, session])

  const episodes = useMemo(
    () => allEpisodes.filter((e) => !seasonId || e.SeasonId === seasonId),
    [allEpisodes, seasonId],
  )

  if (loading) {
    return (
      <div className="detail-loading">
        <Spinner label={t("Loading content…")} />
      </div>
    )
  }

  if (error || !item) {
    return (
      <div className="page-pad">
        <EmptyState
          title={t("Item not found")}
          description={error || t("It may have been removed from the library.")}
          action={
            <button className="btn btn-ghost" onClick={() => navigate('/')}>{t("Back to home")}</button>
          }
        />
      </div>
    )
  }

  const isSeries = item.Type === 'Series'
  const isSeason = item.Type === 'Season'
  const isEpisode = item.Type === 'Episode'
  const isShowLike = isSeries || isSeason || isEpisode
  const resume = (item.UserData?.PlaybackPositionTicks || 0) > 0
  const playItem = isShowLike ? nextTarget : item
  const progress = playProgress(item)
  const backdrop = item.BackdropImageTags?.length
    ? imageUrl(item.Id, 'Backdrop', 1920)
    : item.ImageTags?.Thumb
      ? imageUrl(item.Id, 'Thumb', 1920)
      : imageUrl(item.Id, 'Primary', 1200)
  const poster = imageUrl(item.Id, 'Primary', 600)
  const logo = item.ImageTags?.Logo ? imageUrl(item.Id, 'Logo', 900) : ''
  const source = item.MediaSources?.[0]
  const video = source?.MediaStreams?.find((s) => s.Type === 'Video')
  const audio = source?.MediaStreams?.find((s) => s.Type === 'Audio')
  const subtitles = source?.MediaStreams?.filter((s) => s.Type === 'Subtitle') || []
  const playerLink = playItem ? `/watch/${playItem.Id}` : ''
  const cameFromPlayer = Boolean(
    (location.state as { fromPlayer?: boolean } | null)?.fromPlayer,
  )
  // Never go back into the player: when the detail page was opened from
  // playback, return to its logical parent instead of the history entry.
  const parentPath =
    isEpisode || isSeason
      ? item.SeriesId
        ? `/item/${item.SeriesId}`
        : '/'
      : item.ParentId
        ? `/library/${item.ParentId}`
        : '/'
  const goBack = () => {
    if (cameFromPlayer) navigate(parentPath)
    else navigate(-1)
  }

  const togglePlayed = async () => {
    if (!session || busy) return
    setBusy(true)
    try {
      const played = !item.UserData?.Played
      const ud = await setPlayed(session.user.Id, item.Id, played)
      setItem({ ...item, UserData: { ...item.UserData, ...ud, Played: played } })
      if (item.Type === 'Series') {
        const res = await getEpisodes(session.user.Id, item.Id)
        setAllEpisodes(res.Items || [])
      }
    } finally {
      setBusy(false)
    }
  }

  const toggleFavorite = async () => {
    if (!session || busy) return
    setBusy(true)
    try {
      const fav = !item.UserData?.IsFavorite
      const ud = await setFavorite(session.user.Id, item.Id, fav)
      setItem({ ...item, UserData: { ...item.UserData, ...ud, IsFavorite: fav } })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={`detail-page ${isShowLike ? 'show-detail' : ''}`}>
      <div className="detail-backdrop" style={{ backgroundImage: `url(${backdrop})` }} />
      <div className="detail-scrim" />
      <button className="back-button" onClick={goBack} aria-label={t("Back")}>
        <IconBack size={20} />
      </button>

      <div className="detail-hero page-pad">
        <div className="detail-poster">
          <img src={poster} alt={item.Name} />
        </div>
        <div className="detail-head">
          {isEpisode && (
            <Link to={`/item/${item.SeriesId}`} className="detail-series-link">
              {item.SeriesName}
            </Link>
          )}
          {logo && !isEpisode && !isSeason ? (
            <img className="detail-logo" src={logo} alt={item.Name} />
          ) : (
            <h1>{item.Name}</h1>
          )}
          {item.Taglines?.[0] && <p className="detail-tagline">{item.Taglines[0]}</p>}
          <div className="detail-meta">
            {item.ProductionYear ? <span>{item.ProductionYear}</span> : null}
            {item.CommunityRating ? (
              <span className="rating-inline">
                <IconStar size={13} /> {formatRating(item.CommunityRating)}
              </span>
            ) : null}
            {item.OfficialRating ? <span className="pill">{item.OfficialRating}</span> : null}
            {item.RunTimeTicks ? <span>{formatRuntime(item.RunTimeTicks)}</span> : null}
            {isSeries && seasons.length > 0 ? <span>{seasons.length}{' '}{t("Season")}</span> : null}
            {isEpisode && (
              <span>{episodeCode(item.ParentIndexNumber, item.IndexNumber)}</span>
            )}
            {item.LocationType === 'Virtual' && <span className="pill demo-pill">{t("Demo content")}</span>}
          </div>
          {item.Genres?.length ? (
            <div className="detail-genres">
              {item.Genres.map((g) => (
                <span key={g}>{g}</span>
              ))}
            </div>
          ) : null}
          <p className="detail-overview">{item.Overview || t("No overview.")}</p>
          <div className="detail-actions">
            <button
              className="btn btn-primary"
              disabled={!playItem}
              onClick={() => playerLink && navigate(playerLink)}
            >
              <IconPlay size={18} />
              {isSeries && playItem
                ? t("Play {0}", { 0: episodeCode(playItem.ParentIndexNumber, playItem.IndexNumber) })
                : resume
                  ? t("Continue watching")
                  : t("Play")}
            </button>
            <button className={`icon-action ${item.UserData?.Played ? 'is-on' : ''}`} onClick={togglePlayed} disabled={busy}>
              <IconCheck size={18} /> {item.UserData?.Played ? t("Watched") : t("Mark as watched")}
            </button>
            <button
              className={`icon-action ${item.UserData?.IsFavorite ? 'is-on' : ''}`}
              onClick={toggleFavorite}
              disabled={busy}
            >
              {item.UserData?.IsFavorite ? <IconStar size={17} /> : <IconPlus size={17} />}
              {item.UserData?.IsFavorite ? t("Favorited") : t("Favorite")}
            </button>
          </div>
          {progress > 0 && !isSeries && (
            <div className="detail-progress">
              <span style={{ width: `${progress}%` }} />
            </div>
          )}
        </div>
      </div>

      <div className="detail-body page-pad">
        {isSeries && (
          <section className="episodes-section">
            <div className="section-head">
              <h2>{t("Series")}</h2>
              <span className="muted">{allEpisodes.length}{' '}{t("Ep")}</span>
            </div>
            <div className="season-tabs">
              {seasons.map((s) => (
                <button
                  key={s.Id}
                  className={s.Id === seasonId ? 'active' : ''}
                  onClick={() => setSeasonId(s.Id)}
                >
                  {seasonLabel(s.Name, s.IndexNumber)}
                </button>
              ))}
            </div>
            <div className="episode-list">
              {episodes.map((ep) => (
                <EpisodeRow
                  key={ep.Id}
                  episode={ep}
                  onPlay={() => navigate(`/watch/${ep.Id}`)}
                  onDetails={() => navigate(`/item/${ep.Id}`)}
                />
              ))}
              {!episodes.length && <p className="muted">{t("No episodes in this season yet.")}</p>}
            </div>
          </section>
        )}

        {(isSeason || isEpisode) && (
          <section className="episodes-section">
            {isEpisode && playItem && playItem.Id !== item.Id && (
              <div className="next-inline">
                <span>{t("Next up")}</span>
                <button className="text-button" onClick={() => navigate(`/watch/${playItem.Id}`)}>{t("Play")}{' '}{episodeCode(playItem.ParentIndexNumber, playItem.IndexNumber)}
                </button>
              </div>
            )}
            {isSeason && (
              <>
                <div className="section-head">
                  <h2>{item.Name}</h2>
                  <span className="muted">{episodes.length}{' '}{t("Ep")}</span>
                </div>
                <div className="episode-list">
                  {episodes.map((ep) => (
                    <EpisodeRow
                      key={ep.Id}
                      episode={ep}
                      onPlay={() => navigate(`/watch/${ep.Id}`)}
                      onDetails={() => navigate(`/item/${ep.Id}`)}
                    />
                  ))}
                </div>
              </>
            )}
          </section>
        )}

        {item.People && item.People.length > 0 && (
          <section className="cast-section">
            <h2>{t("Cast")}</h2>
            <div className="cast-row">
              {item.People.map((p, i) => (
                <div className="cast-card" key={`${p.Id || p.Name}-${i}`}>
                  <div className="cast-avatar">
                    <span>{initials(p.Name)}</span>
                    {p.ImageUrl && (
                      <img
                        src={p.ImageUrl}
                        alt=""
                        loading="lazy"
                        onError={(e) => {
                          e.currentTarget.style.display = 'none'
                        }}
                      />
                    )}
                  </div>
                  <div className="cast-name">{p.Name}</div>
                  {p.Role && <div className="cast-role">{p.Role}</div>}
                </div>
              ))}
            </div>
          </section>
        )}

        <section className="facts-section">
          <h2>{isSeries || isSeason ? t("Series info") : t("Technical info")}</h2>
          <div className="facts-grid">
            {isSeries ? (
              <>
                <Fact label={t("Seasons")} value={String(seasons.length)} />
                <Fact label={t("Total episodes")} value={String(allEpisodes.length)} />
                <Fact label={t("Type")} value={t("Series")} />
                {item.OfficialRating ? <Fact label={t("Rating")} value={item.OfficialRating} /> : null}
              </>
            ) : isSeason ? (
              <>
                <Fact label={t("Episodes")} value={String(episodes.length)} />
                <Fact label={t("Part of series")} value={item.SeriesName || '—'} />
              </>
            ) : (
              <>
                <Fact label={t("Container")} value={prettyContainer(item.Container || source?.Container || '—')} />
                {video && (
                  <>
                    <Fact
                      label={t("Video")}
                      value={`${video.Width && video.Height ? `${video.Width} × ${video.Height}` : '—'}${
                        video.Codec ? ` · ${video.Codec.toUpperCase()}` : ''
                      }`}
                    />
                    <Fact label={t("Video stream")} value={formatBitrate(item.MediaSources?.[0]?.Bitrate)} />
                  </>
                )}
                {audio && (
                  <Fact
                    label={t("Audio")}
                    value={`${(audio.DisplayLanguage || audio.Language || t("Unknown"))} · ${(audio.Codec || '').toUpperCase()}${
                      audio.Channels ? t(" · {0} channels", { 0: audio.Channels }) : ''
                    }`}
                  />
                )}
                <Fact label={t("Subtitles")} value={subtitles.length ? t("{0} items", { 0: subtitles.length }) : t("None")} />
                {item.Size ? <Fact label={t("File size")} value={formatSize(item.Size)} /> : null}
                {item.Path && !item.Path.startsWith('demo://') ? (
                  <Fact label={t("File")} value={item.Path} wide />
                ) : null}
              </>
            )}
          </div>
        </section>

        {similar.length > 0 && <MediaRow title={t("Similar titles")} items={similar} />}
      </div>
    </div>
  )
}

function EpisodeRow({
  episode,
  onPlay,
  onDetails,
}: {
  episode: JFItem
  onPlay: () => void
  onDetails: () => void
}) {
  const progress = playProgress(episode)
  return (
    <div className="episode-row">
      <button className="episode-main" onClick={onPlay}>
        <div className="episode-thumb">
          <img src={imageUrl(episode.Id, episode.ImageTags?.Thumb ? 'Thumb' : 'Backdrop', 480)} alt="" loading="lazy" />
          <span className="episode-number">{episode.IndexNumber ?? '·'}</span>
          <span className="episode-play">
            <IconPlay size={15} />
          </span>
          {progress > 0 && (
            <span className="episode-progress">
              <i style={{ width: `${progress}%` }} />
            </span>
          )}
        </div>
        <div className="episode-info">
          <div className="episode-top">
            <span className="episode-title">{episode.Name}</span>
            <span className="episode-runtime">{formatRuntime(episode.RunTimeTicks, true)}</span>
          </div>
          <p>{episode.Overview || t("No overview for this episode.")}</p>
          {episode.UserData?.Played && <span className="episode-watched">{t("Watched")}</span>}
        </div>
      </button>
      <button
        className="episode-details"
        onClick={onDetails}
        aria-label={t("Details")}
        title={t("Details")}
      >
        <IconInfo size={17} />
      </button>
    </div>
  )
}

function Fact({ label, value, wide }: { label: string; value: string; wide?: boolean }) {
  return (
    <div className={`fact ${wide ? 'wide' : ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

function formatBitrate(bps?: number): string {
  if (!bps) return '—'
  return `${(bps / 1_000_000).toFixed(1)} Mbps`
}

function formatSize(bytes: number): string {
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}
