import { t, useI18n } from '../lib/i18n'

const TMDB_URL = 'https://www.themoviedb.org/'
// Required notice, quoted from paragraph 3 of the TMDB API Terms of Use.
const NOTICE = 'This product uses TMDB and the TMDB APIs but is not endorsed, certified, or otherwise approved by TMDB.'

export function TmdbAttribution() {
  const { lang } = useI18n()
  return (
    <div className="tmdb-attribution">
      <a
        className="tmdb-attribution-logo"
        href={TMDB_URL}
        target="_blank"
        rel="noopener noreferrer"
      >
        <img src="/tmdb-logo.svg" alt="TMDB" width={184} height={13} />
      </a>
      <div className="tmdb-attribution-copy">
        <p className="tmdb-attribution-notice">{NOTICE}</p>
        {lang !== 'en' && <p className="tmdb-attribution-translation">{t(NOTICE)}</p>}
      </div>
    </div>
  )
}
