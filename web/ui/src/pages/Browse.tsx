import { t } from '../lib/i18n'
import { Link } from 'react-router-dom'
import { useViews, useViewsLoading } from '../components/Layout'
import { EmptyState } from '../components/Media'
import { IconChevronRight, IconGrid, IconLayers, IconPlus } from '../components/Icons'
import { useAuth } from '../lib/auth'

function hueFor(id: string): number {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) % 360
  return h
}

function typeLabel(type: string): string {
  if (type === 'movies') return t("Movies")
  if (type === 'tvshows') return t("Series")
  if (type === 'mixed') return t("Mixed")
  return type
}

export function BrowsePage() {
  const views = useViews()
  const viewsLoading = useViewsLoading()
  const { isAdmin } = useAuth()

  return (
    <div className="page-pad browse-page">
      <div className="page-heading">
        <div>
          <div className="eyebrow">
            <IconLayers size={16} />{' '}{t("Media libraries")}</div>
          <h1>{t("Library")}</h1>
          <p className="muted">
            {viewsLoading ? t("Loading…") : <>{views.length}{' '}{t("libraries")}</>}
          </p>
        </div>
      </div>

      {viewsLoading ? (
        <div className="library-grid">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="library-card skeleton" />
          ))}
        </div>
      ) : !views.length ? (
        <EmptyState title={t("No libraries yet")} description={t("Your movies and series will appear here when the scan finishes.")} />
      ) : (
        <div className="library-grid">
          {views.map((v) => (
            <Link
              key={v.Id}
              to={`/library/${v.Id}`}
              className="library-card"
              style={{ ['--hue' as string]: hueFor(v.Id) }}
            >
              <div className="library-card-glow" />
              <div className="library-icon">
                {v.CollectionType === 'movies' ? <IconGrid size={22} /> : <IconLayers size={22} />}
              </div>
              <div className="library-name">{v.Name}</div>
              <div className="library-meta">
                {typeLabel(v.CollectionType || 'mixed')} ·{' '}
                {v.ChildCount ?? 0}{' '}{t("items")}</div>
              <div className="library-open">{t("Open library")}{' '}<IconChevronRight size={16} />
              </div>
            </Link>
          ))}
          {isAdmin && (
            <Link to="/settings#server" className="library-card add-library-card">
              <div className="library-icon">
                <IconPlus size={22} />
              </div>
              <div className="library-name">{t("Add library")}</div>
              <div className="library-meta">{t("Add a folder and scan automatically")}</div>
            </Link>
          )}
        </div>
      )}
    </div>
  )
}
