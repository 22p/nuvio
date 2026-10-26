import { createContext, Fragment, useContext, useMemo, useState, type ReactNode } from 'react'
import { zh } from './locales/zh'
import { zhTW } from './locales/zhTW'
import { ja } from './locales/ja'
import { ko } from './locales/ko'

export type Lang = 'en' | 'zh' | 'zh-TW' | 'ja' | 'ko'

export const languages: { value: Lang; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'zh', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
]

const KEY = 'nuvio.lang'
const htmlLang: Record<Lang, string> = { en: 'en', zh: 'zh-CN', 'zh-TW': 'zh-TW', ja: 'ja', ko: 'ko' }
const catalogs: Record<Lang, Record<string, string>> = { en: {}, zh, 'zh-TW': zhTW, ja, ko }

function detect(): Lang {
  try {
    const saved = localStorage.getItem(KEY)
    if (saved && (saved === 'en' || saved === 'zh' || saved === 'zh-TW' || saved === 'ja' || saved === 'ko')) {
      return saved
    }
    for (const tag of navigator.languages || [navigator.language]) {
      if (!tag) continue
      const value = tag.toLowerCase()
      if (value.startsWith('zh')) {
        return /hant|tw|hk|mo/.test(value) ? 'zh-TW' : 'zh'
      }
      if (value.startsWith('ja')) return 'ja'
      if (value.startsWith('ko')) return 'ko'
      if (value.startsWith('en')) return 'en'
    }
  } catch {
    /* storage unavailable */
  }
  return 'en'
}

let current: Lang = detect()
document.documentElement.lang = htmlLang[current]

export function getLang(): Lang {
  return current
}

export function setLang(lang: Lang) {
  current = lang
  try {
    localStorage.setItem(KEY, lang)
  } catch {
    /* storage unavailable */
  }
  document.documentElement.lang = htmlLang[lang]
}

/**
 * Translate a UI string. English is the source language and therefore also
 * the key; missing translations gracefully fall back to English. Placeholders
 * use `{name}` and are filled from `vars`.
 */
export function t(key: string, vars?: Record<string, string | number>): string {
  let out = catalogs[current]?.[key] ?? key
  if (vars) {
    for (const [name, value] of Object.entries(vars)) {
      out = out.split(`{${name}}`).join(String(value))
    }
  }
  return out
}

interface I18nValue {
  lang: Lang
  setLang: (lang: Lang) => void
}

const I18nContext = createContext<I18nValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(getLang)

  const value = useMemo<I18nValue>(
    () => ({
      lang,
      setLang: (next) => {
        setLang(next)
        setLangState(next)
      },
    }),
    [lang],
  )

  // Remount the tree so every rendered string is recomputed for the locale.
  return (
    <I18nContext.Provider value={value}>
      <Fragment key={lang}>{children}</Fragment>
    </I18nContext.Provider>
  )
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error('useI18n must be used inside I18nProvider')
  return ctx
}
