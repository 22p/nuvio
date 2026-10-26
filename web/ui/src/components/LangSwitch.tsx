import { languages, useI18n, type Lang } from '../lib/i18n'

export function LangSwitch() {
  const { lang, setLang } = useI18n()
  return (
    <label className="select-wrap compact auth-lang">
      <select value={lang} onChange={(e) => setLang(e.target.value as Lang)} aria-label="Language">
        {languages.map((l) => (
          <option key={l.value} value={l.value}>
            {l.label}
          </option>
        ))}
      </select>
    </label>
  )
}
