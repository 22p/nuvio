import { t } from '../lib/i18n'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getItems } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { JFItem } from '../lib/types'
import { EmptyState, PosterCard } from '../components/Media'
import { IconHeart } from '../components/Icons'

export function FavoritesPage() {
  const { session } = useAuth()
  const [items, setItems] = useState<JFItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!session) return
    let alive = true
    setLoading(true)
    getItems(session.user.Id, {
      filters: 'IsFavorite',
      recursive: true,
      includeItemTypes: 'Movie,Series,Episode',
      sortBy: 'DateCreated',
      sortOrder: 'Descending',
      limit: 200,
    })
      .then((res) => {
        if (!alive) return
        setItems(res.Items || [])
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
  }, [session])

  return (
    <div className="page-pad favorites-page">
      <div className="page-heading">
        <div>
          <div className="eyebrow">
            <IconHeart size={16} />{' '}{t("Favorites")}
          </div>
          <h1>{t("Favorites")}</h1>
          <p className="muted">
            {loading ? t("Loading…") : <>{items.length}{' '}{t("items")}</>}
          </p>
        </div>
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
          title={t("No favorites yet")}
          description={t("Open a movie or series and press Favorite to keep it here.")}
          action={
            <Link to="/browse" className="btn btn-ghost">
              {t("Browse library")}
            </Link>
          }
        />
      ) : (
        <div className="grid">
          {items.map((item) => (
            <PosterCard key={item.Id} item={item} />
          ))}
        </div>
      )}
    </div>
  )
}
