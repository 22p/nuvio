import { t } from '../lib/i18n'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { searchItems } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { JFItem } from '../lib/types'
import { EmptyState, PosterCard } from '../components/Media'
import { IconClose, IconSearch } from '../components/Icons'

const RECENT_KEY = 'nuvio.recentSearches'

function loadRecent(): string[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]').slice(0, 8)
  } catch {
    return []
  }
}

export function SearchPage() {
  const { session } = useAuth()
  const [params, setParams] = useSearchParams()
  const initial = params.get('q') || ''
  const [query, setQuery] = useState(initial)
  const [results, setResults] = useState<JFItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [recent, setRecent] = useState<string[]>(loadRecent)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    const term = query.trim()
    if (!term) {
      setResults([])
      setTotal(0)
      return
    }
    let alive = true
    setLoading(true)
    const timer = window.setTimeout(async () => {
      try {
        const res = await searchItems(session!.user.Id, term)
        if (!alive) return
        setResults(res.Items || [])
        setTotal(res.TotalRecordCount || 0)
      } catch {
        if (alive) {
          setResults([])
          setTotal(0)
        }
      } finally {
        if (alive) setLoading(false)
      }
    }, 320)
    return () => {
      alive = false
      window.clearTimeout(timer)
    }
  }, [query, session])

  useEffect(() => {
    const term = query.trim()
    if (!term) return
    const timer = window.setTimeout(() => {
      setParams({ q: term }, { replace: true })
      setRecent((prev) => {
        const next = [term, ...prev.filter((t) => t !== term)].slice(0, 8)
        localStorage.setItem(RECENT_KEY, JSON.stringify(next))
        return next
      })
    }, 900)
    return () => window.clearTimeout(timer)
  }, [query, setParams])

  const grouped = useMemo(() => {
    const movies = results.filter((r) => r.Type === 'Movie')
    const series = results.filter((r) => r.Type === 'Series')
    const episodes = results.filter((r) => r.Type === 'Episode')
    return { movies, series, episodes }
  }, [results])

  const hasQuery = query.trim().length > 0
  const hasResults = results.length > 0

  return (
    <div className="page-pad search-page">
      <div className="search-head">
        <div className="eyebrow">{t("Search all libraries")}</div>
        <h1>{t("Search")}</h1>
        <div className="search-field">
          <IconSearch size={20} />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("Search movies, series, episodes…")}
            spellCheck={false}
            aria-label={t("Search")}
          />
          {query && (
            <button className="search-clear" onClick={() => setQuery('')} aria-label={t("Clear all")}>
              <IconClose size={16} />
            </button>
          )}
        </div>
        {hasQuery && !loading && (
          <div className="search-count">{total}{' '}{t("results")}</div>
        )}
      </div>

      {!hasQuery ? (
        <div className="search-idle">
          {recent.length > 0 && (
            <div className="recent-searches">
              <div className="recent-head">
                <span>{t("Recent searches")}</span>
                <button
                  className="text-button"
                  onClick={() => {
                    localStorage.removeItem(RECENT_KEY)
                    setRecent([])
                  }}
                >{t("Clear")}</button>
              </div>
              <div className="chips">
                {recent.map((term) => (
                  <button key={term} className="chip" onClick={() => setQuery(term)}>
                    {term}
                  </button>
                ))}
              </div>
            </div>
          )}
          <div className="search-hint">
            <span className="search-orb" />
            <h3>{t("Find something to watch")}</h3>
            <p>{t("Search matches titles, series and episode names.")}</p>
          </div>
        </div>
      ) : loading ? (
        <div className="grid">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="skeleton skeleton-poster" />
          ))}
        </div>
      ) : !hasResults ? (
        <EmptyState title={t("No results")} description={t("Nothing matches “{0}”.", { 0: query.trim() })} />
      ) : (
        <div className="search-results">
          {grouped.movies.length > 0 && (
            <ResultGroup title={t("Movies")} items={grouped.movies} />
          )}
          {grouped.series.length > 0 && (
            <ResultGroup title={t("Series")} items={grouped.series} />
          )}
          {grouped.episodes.length > 0 && (
            <ResultGroup title={t("Episode")} items={grouped.episodes} />
          )}
        </div>
      )}
    </div>
  )
}

function ResultGroup({ title, items }: { title: string; items: JFItem[] }) {
  return (
    <section className="result-group">
      <div className="section-head">
        <h2>{title}</h2>
        <span className="muted">{items.length}</span>
      </div>
      <div className="grid">
        {items.map((item) => (
          <PosterCard key={item.Id} item={item} />
        ))}
      </div>
    </section>
  )
}
