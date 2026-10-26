import { t } from '../lib/i18n'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { getItems, getScanStatus } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { JFItem } from '../lib/types'
import { useViews } from '../components/Layout'
import { EmptyState, PosterCard, RowSkeleton } from '../components/Media'
import { IconGrid, IconLayers } from '../components/Icons'

const SORTS = [
  { value: 'DateCreated:Descending', label: 'Recently added' },
  { value: 'SortName:Ascending', label: 'Name A-Z' },
  { value: 'CommunityRating:Descending', label: 'Top rated' },
  { value: 'ProductionYear:Descending', label: 'Newest' },
]

export function LibraryPage() {
  const { id = '' } = useParams()
  const { session } = useAuth()
  const views = useViews()
  const view = views.find((v) => v.Id === id)
  const [items, setItems] = useState<JFItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [sort, setSort] = useState(SORTS[0].value)
  const [typeFilter, setTypeFilter] = useState<'all' | 'Movie' | 'Series'>('all')
  const [page, setPage] = useState(0)
  const [scanTick, setScanTick] = useState(0)
  const emptyChecked = useRef('')

  const collection = view?.CollectionType || 'mixed'
  const types = useMemo(() => {
    if (typeFilter !== 'all') return typeFilter
    if (collection === 'movies') return 'Movie'
    if (collection === 'tvshows') return 'Series'
    return undefined
  }, [collection, typeFilter])

  useEffect(() => {
    if (!session || !id) return
    let alive = true
    setLoading(true)
    setItems([])
    setPage(0)
    const [sortBy, sortOrder] = sort.split(':')
    getItems(session.user.Id, {
      parentId: id,
      includeItemTypes: types,
      sortBy,
      sortOrder,
      recursive: false,
      startIndex: 0,
      limit: 60,
    })
      .then((res) => {
        if (!alive) return
        setItems(res.Items || [])
        setTotal(res.TotalRecordCount || 0)
        setError('')
      })
      .catch((err) => {
        if (alive) setError(err instanceof Error ? err.message : t("Loading failed"))
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [session, id, sort, types, scanTick])

  useEffect(() => {
    if (loading || total > 0 || !session || emptyChecked.current === id) return
    let alive = true
    let timer = 0
    const check = async () => {
      const status = await getScanStatus().catch(() => null)
      if (!alive) return
      if (status?.scanning) {
        timer = window.setTimeout(check, 2500)
      } else {
        emptyChecked.current = id
        setScanTick((t) => t + 1)
      }
    }
    check()
    return () => {
      alive = false
      window.clearTimeout(timer)
    }
  }, [loading, total, session, id])

  const loadMore = async () => {
    if (!session || loadingMore) return
    setLoadingMore(true)
    const [sortBy, sortOrder] = sort.split(':')
    const next = page + 1
    try {
      const res = await getItems(session.user.Id, {
        parentId: id,
        includeItemTypes: types,
        sortBy,
        sortOrder,
        recursive: false,
        startIndex: next * 60,
        limit: 60,
      })
      setItems((prev) => [...prev, ...(res.Items || [])])
      setTotal(res.TotalRecordCount || 0)
      setPage(next)
    } catch {
      /* keep current page */
    } finally {
      setLoadingMore(false)
    }
  }

  const heading = view?.Name || t("Media libraries")
  const isShows = collection === 'tvshows'

  return (
    <div className="library-page">
      <header className="library-hero">
        <div className="library-hero-bg" />
        <div className="library-hero-content page-pad">
          <div className="eyebrow">{isShows ? t("Series") : t("Movies")}{t("Library")}</div>
          <h1>{heading}</h1>
          <p className="muted">
            {total || view?.ChildCount || 0}{' '}{t("items")}</p>
        </div>
      </header>

      <div className="page-pad">
        <div className="library-toolbar">
          <div className="chips">
            {collection === 'mixed' && (
              <>
                <button
                  className={typeFilter === 'all' ? 'chip active' : 'chip'}
                  onClick={() => setTypeFilter('all')}
                >{t("All")}</button>
                <button
                  className={typeFilter === 'Movie' ? 'chip active' : 'chip'}
                  onClick={() => setTypeFilter('Movie')}
                >{t("Movies")}</button>
                <button
                  className={typeFilter === 'Series' ? 'chip active' : 'chip'}
                  onClick={() => setTypeFilter('Series')}
                >{t("Series")}</button>
              </>
            )}
            <span className="chip chip-static">
              {isShows ? <IconLayers size={14} /> : <IconGrid size={14} />}
              {isShows ? t("Series") : collection === 'movies' ? t("Movies") : t("Mixed")}
            </span>
          </div>
          <label className="select-wrap">
            <span>{t("Sort")}</span>
            <select value={sort} onChange={(e) => setSort(e.target.value)}>
              {SORTS.map((s) => (
                <option key={s.value} value={s.value}>
                  {t(s.label)}
                </option>
              ))}
            </select>
          </label>
        </div>

        {error && <div className="inline-error">{t(error)}</div>}

        {loading ? (
          <div className="grid">
            {Array.from({ length: 12 }).map((_, i) => (
              <div key={i} className="skeleton skeleton-poster" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <EmptyState
            title={t("Nothing here yet")}
            description={t("If you just added the library, wait for the scan to finish. Also check that the folder contains video files.")}
          />
        ) : (
          <>
            <div className="grid">
              {items.map((item) => (
                <PosterCard key={item.Id} item={item} />
              ))}
            </div>
            {items.length < total && (
              <div className="load-more">
                <button className="btn btn-ghost" onClick={loadMore} disabled={loadingMore}>
                  {loadingMore ? t("Loading…") : t("Load more ({0}/{1})", { 0: items.length, 1: total })}
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

export function LibrarySkeleton() {
  return (
    <div className="page-pad">
      <RowSkeleton />
    </div>
  )
}
