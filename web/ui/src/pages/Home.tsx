import { t } from '../lib/i18n'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { getLatest, getNextUp, getResume } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { JFItem } from '../lib/types'
import { useViews } from '../components/Layout'
import { EmptyState, Hero, MediaRow, RowSkeleton } from '../components/Media'
import { IconLayers, IconSettings } from '../components/Icons'

export function HomePage() {
  const { session, isAdmin, config } = useAuth()
  const views = useViews()
  const [resume, setResume] = useState<JFItem[]>([])
  const [nextUp, setNextUp] = useState<JFItem[]>([])
  const [latestByView, setLatestByView] = useState<Record<string, JFItem[]>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!session || views.length === 0) {
      if (!views.length) setLoading(false)
      return
    }
    let alive = true
    setLoading(true)
    Promise.all([
      getResume(session.user.Id, 16).catch(() => ({ Items: [] as JFItem[], TotalRecordCount: 0, StartIndex: 0 })),
      getNextUp(session.user.Id, 16).catch(() => ({ Items: [] as JFItem[], TotalRecordCount: 0, StartIndex: 0 })),
      ...views.map((v) =>
        getLatest(session.user.Id, v.Id, 18).catch(() => [] as JFItem[]),
      ),
    ])
      .then((results) => {
        if (!alive) return
        const r = results[0] as { Items: JFItem[] }
        const n = results[1] as { Items: JFItem[] }
        setResume(r.Items || [])
        setNextUp(n.Items || [])
        const map: Record<string, JFItem[]> = {}
        views.forEach((v, i) => {
          map[v.Id] = (results[i + 2] as JFItem[]) || []
        })
        setLatestByView(map)
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
  }, [session, views])

  const moviesView = views.find((v) => v.CollectionType === 'movies')
  const showsView = views.find((v) => v.CollectionType === 'tvshows')
  const latestMovies = moviesView ? latestByView[moviesView.Id] || [] : []
  const latestShows = showsView ? latestByView[showsView.Id] || [] : []
  const featured = useMemo(() => {
    const pool = latestMovies.length
      ? latestMovies
      : Object.values(latestByView).flat()
    const preferred = pool.filter((i) => i.Type === 'Movie' || i.Type === 'Series')
    const list = preferred.length ? preferred : pool
    return [...list]
      .sort((a, b) => (b.CommunityRating || 0) - (a.CommunityRating || 0))
      .slice(0, 5)
  }, [latestByView, latestMovies])

  if (!views.length) {
    if (config?.scanning) {
      return (
        <div className="home page-pad">
          <div className="hero-skeleton skeleton" />
          <RowSkeleton />
          <RowSkeleton />
        </div>
      )
    }
    return (
      <div className="page-pad">
        <EmptyState
          title={t("Your library is empty")}
          description={
            isAdmin
              ? t("Add a media folder and Nuvio will scan its movies and series. You can also restart and keep the demo library.")
              : t("Ask an administrator to add a media folder in Settings.")
          }
          action={
            isAdmin ? (
              <Link to="/settings#server" className="btn btn-primary">
                <IconSettings size={18} />{' '}{t("Open server settings")}</Link>
            ) : undefined
          }
        />
      </div>
    )
  }

  return (
    <div className="home">
      {error && <div className="inline-error">{t(error)}</div>}
      {loading && !featured.length ? (
        <div className="page-pad">
          <div className="hero-skeleton skeleton" />
          <RowSkeleton />
          <RowSkeleton />
        </div>
      ) : (
        <>
          <Hero items={featured} />
          <div className="home-rows">
            {resume.length > 0 && (
              <MediaRow title={t("Continue watching")} items={resume} variant="continue" />
            )}
            {nextUp.length > 0 && <MediaRow title={t("Next up")} items={nextUp} variant="wide" />}
            {latestMovies.length > 0 && (
              <MediaRow
                title={t("Latest movies")}
                items={latestMovies}
                moreHref={moviesView ? `/library/${moviesView.Id}` : undefined}
              />
            )}
            {latestShows.length > 0 && (
              <MediaRow
                title={t("Recently added series")}
                items={latestShows}
                variant="wide"
                moreHref={showsView ? `/library/${showsView.Id}` : undefined}
              />
            )}
            {views
              .filter((v) => v.CollectionType !== 'movies' && v.CollectionType !== 'tvshows')
              .map((v) => (
                <MediaRow
                  key={v.Id}
                  title={v.Name}
                  items={latestByView[v.Id] || []}
                  moreHref={`/library/${v.Id}`}
                />
              ))}
            {!resume.length && !nextUp.length && !latestMovies.length && !latestShows.length && (
              <EmptyState
                title={t("Nothing to play yet")}
                description={t("The library is being scanned. Check back in a moment.")}
                action={
                  <Link to="/browse" className="btn btn-ghost">
                    <IconLayers size={18} />{' '}{t("Browse library")}</Link>
                }
              />
            )}
          </div>
        </>
      )}
    </div>
  )
}
