import { t } from '../lib/i18n'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { imageUrl } from '../lib/api'
import {
  episodeCode,
  formatRating,
  formatRuntime,
  mediaYear,
  playProgress,
  progressPercent,
  ticksToSeconds,
} from '../lib/format'
import type { JFItem } from '../lib/types'
import {
  IconChevronLeft,
  IconChevronRight,
  IconInfo,
  IconPlay,
  IconStar,
} from './Icons'

function hueFor(id: string): number {
  let h = 0
  for (let i = 0; i < Math.min(id.length, 8); i++) h = (h * 31 + id.charCodeAt(i)) % 360
  return h
}

function hasPrimary(item: JFItem): boolean {
  return Boolean(item.ImageTags?.Primary)
}

function primaryMeta(item: JFItem): string {
  if (item.Type === 'Episode') {
    return [item.SeriesName, episodeCode(item.ParentIndexNumber, item.IndexNumber)]
      .filter(Boolean)
      .join(' · ')
  }
  if (item.Type === 'Series') {
    const seasons = item.ChildCount || 0
    return [mediaYear(item), seasons ? t("{0} seasons", { 0: seasons }) : t("Series")].filter(Boolean).join(' · ')
  }
  return [mediaYear(item), formatRuntime(item.RunTimeTicks, true)].filter(Boolean).join(' · ')
}

interface CardProps {
  item: JFItem
  wide?: boolean
}

export function PosterCard({ item, wide = false }: CardProps) {
  const [failed, setFailed] = useState(false)
  const progress = playProgress(item)
  const img = imageUrl(item.Id, wide ? 'Backdrop' : 'Primary', wide ? 800 : 440)

  return (
    <Link to={`/item/${item.Id}`} className={`media-card ${wide ? 'is-wide' : ''}`}>
      <div
        className={`media-art ${failed || !hasPrimary(item) ? 'has-fallback' : ''}`}
        style={{ ['--hue' as string]: hueFor(item.Id) }}
      >
        {!failed && hasPrimary(item) && (
          <img src={img} alt="" loading="lazy" onError={() => setFailed(true)} />
        )}
        {(failed || !hasPrimary(item)) && (
          <div className="art-fallback">
            <span>{item.Name.slice(0, 1)}</span>
          </div>
        )}
        <div className="art-vignette" />
        <div className="art-badges">
          {item.CommunityRating ? (
            <span className="badge rating">
              <IconStar size={11} /> {formatRating(item.CommunityRating)}
            </span>
          ) : null}
          {item.LocationType === 'Virtual' && <span className="badge demo">{t("Demo")}</span>}
          {item.Type === 'Episode' && (
            <span className="badge code">{episodeCode(item.ParentIndexNumber, item.IndexNumber)}</span>
          )}
        </div>
        <div className="art-play">
          <IconPlay size={18} />
        </div>
        {progress > 0 && (
          <div className="art-progress">
            <span style={{ width: `${progress}%` }} />
          </div>
        )}
      </div>
      <div className="media-info">
        <div className="media-title">{item.Name}</div>
        <div className="media-meta">{primaryMeta(item)}</div>
      </div>
    </Link>
  )
}

export function ContinueCard({ item }: { item: JFItem }) {
  const progress = playProgress(item)
  const remaining = Math.max(0, ticksToSeconds(item.RunTimeTicks) - ticksToSeconds(item.UserData?.PlaybackPositionTicks))
  const label =
    item.Type === 'Episode'
      ? `${item.SeriesName || ''} · ${episodeCode(item.ParentIndexNumber, item.IndexNumber)}`
      : primaryMeta(item)
  return (
    <Link to={`/watch/${item.Id}`} className="continue-card">
      <img src={imageUrl(item.Id, 'Backdrop', 700)} alt="" loading="lazy" />
      <div className="continue-shade" />
      <div className="continue-body">
        <div className="continue-label">{label}</div>
        <div className="continue-title">{item.Name}</div>
        <div className="continue-meta">
          {item.LocationType === 'Virtual'
            ? t("Demo clip · {0}% watched", { 0: Math.round(progress) })
            : t("{0} min left", { 0: Math.max(1, Math.round(remaining / 60)) })}
        </div>
        <div className="continue-progress">
          <span style={{ width: `${progress}%` }} />
        </div>
      </div>
      <span className="continue-play">
        <IconPlay size={16} />
      </span>
    </Link>
  )
}

interface RowProps {
  title: string
  items: JFItem[]
  variant?: 'poster' | 'wide' | 'continue'
  moreHref?: string
}

export function MediaRow({ title, items, variant = 'poster', moreHref }: RowProps) {
  const ref = useRef<HTMLDivElement>(null)
  const [atStart, setAtStart] = useState(true)
  const [atEnd, setAtEnd] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => {
      setAtStart(el.scrollLeft <= 4)
      setAtEnd(el.scrollLeft + el.clientWidth >= el.scrollWidth - 4)
    }
    update()
    el.addEventListener('scroll', update, { passive: true })
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => {
      el.removeEventListener('scroll', update)
      ro.disconnect()
    }
  }, [items.length])

  if (!items.length) return null

  const scroll = (dir: number) => {
    const el = ref.current
    if (!el) return
    el.scrollBy({ left: dir * el.clientWidth * 0.82, behavior: 'smooth' })
  }

  return (
    <section className={`media-row ${variant}`}>
      <div className="row-head">
        <h2>{title}</h2>
        {moreHref && (
          <Link to={moreHref} className="row-more">{t("View all")}{' '}<IconChevronRight size={15} />
          </Link>
        )}
      </div>
      <div className="row-wrap">
        <button
          className={`row-arrow left ${atStart ? 'is-hidden' : ''}`}
          onClick={() => scroll(-1)}
          aria-label={t("Scroll left")}
        >
          <IconChevronLeft size={22} />
        </button>
        <div className={`row-scroll ${variant}`} ref={ref}>
          {items.map((item) =>
            variant === 'continue' ? (
              <ContinueCard key={item.Id} item={item} />
            ) : (
              <PosterCard key={item.Id} item={item} wide={variant === 'wide'} />
            ),
          )}
        </div>
        <button
          className={`row-arrow right ${atEnd ? 'is-hidden' : ''}`}
          onClick={() => scroll(1)}
          aria-label={t("Scroll right")}
        >
          <IconChevronRight size={22} />
        </button>
      </div>
    </section>
  )
}

export function Hero({ items }: { items: JFItem[] }) {
  const navigate = useNavigate()
  const [index, setIndex] = useState(0)
  const [paused, setPaused] = useState(false)

  useEffect(() => {
    if (paused || items.length <= 1) return
    const timer = window.setInterval(() => setIndex((i) => (i + 1) % items.length), 14000)
    return () => window.clearInterval(timer)
  }, [paused, items.length])

  useEffect(() => {
    if (index >= items.length) setIndex(0)
  }, [items.length, index])

  if (!items.length) return null
  const item = items[Math.min(index, items.length - 1)]
  const resume = (item.UserData?.PlaybackPositionTicks || 0) > 0
  const progress = playProgress(item)

  return (
    <section
      className="hero"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      {items.map((it, i) => (
        <div
          key={it.Id}
          className={`hero-bg ${i === index ? 'active' : ''}`}
          style={{ backgroundImage: `url(${imageUrl(it.Id, 'Backdrop', 1800)})` }}
        />
      ))}
      <div className="hero-scrim" />
      <div className="hero-content" key={item.Id}>
        <div className="hero-eyebrow">
          <span className="live-dot" />
          {item.LocationType === 'Virtual' ? t("NUVIO demo library") : t("NUVIO picks")}
        </div>
        <h1>{item.Name}</h1>
        <div className="hero-meta">
          {item.ProductionYear ? <span>{item.ProductionYear}</span> : null}
          {item.CommunityRating ? (
            <span className="rating-inline">
              <IconStar size={13} /> {formatRating(item.CommunityRating)}
            </span>
          ) : null}
          {item.OfficialRating ? <span className="pill">{item.OfficialRating}</span> : null}
          {item.RunTimeTicks ? <span>{formatRuntime(item.RunTimeTicks)}</span> : null}
          {item.Genres?.slice(0, 3).map((g) => (
            <span key={g} className="genre">
              {g}
            </span>
          ))}
        </div>
        <p className="hero-overview">{item.Overview || item.Taglines?.[0] || t("No overview")}</p>
        <div className="hero-actions">
          <button className="btn btn-primary" onClick={() => navigate(`/watch/${item.Id}`)}>
            <IconPlay size={18} /> {resume ? t("Continue watching") : t("Play")}
          </button>
          <button className="btn btn-ghost" onClick={() => navigate(`/item/${item.Id}`)}>
            <IconInfo size={18} />{' '}{t("Details")}</button>
        </div>
        {progress > 0 && (
          <div className="hero-progress">
            <span style={{ width: `${progress}%` }} />
          </div>
        )}
      </div>
      {items.length > 1 && (
        <div className="hero-dots">
          {items.map((it, i) => (
            <button
              key={it.Id}
              className={i === index ? 'active' : ''}
              onClick={() => setIndex(i)}
              aria-label={t("Recommended {0}", { 0: i + 1 })}
            />
          ))}
        </div>
      )}
    </section>
  )
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string
  description?: string
  action?: ReactNode
}) {
  return (
    <div className="empty-state">
      <div className="empty-art" />
      <h3>{title}</h3>
      {description && <p>{description}</p>}
      {action}
    </div>
  )
}

export function RowSkeleton({ count = 6, wide = false }: { count?: number; wide?: boolean }) {
  return (
    <div className="row-skeleton">
      <div className="skeleton skeleton-title" />
      <div className="row-scroll">
        {Array.from({ length: count }).map((_, i) => (
          <div key={i} className={`skeleton ${wide ? 'skeleton-wide' : 'skeleton-poster'}`} />
        ))}
      </div>
    </div>
  )
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="spinner-wrap">
      <div className="spinner" />
      {label && <span>{label}</span>}
    </div>
  )
}
