import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export interface Prefs {
  /** 0 means "auto" (direct play when possible). */
  maxBitrateMbps: number
  directPlay: boolean
  audioLang: string
  subtitleLang: string
  subtitleMode: 'off' | 'auto' | 'always'
  autoNext: boolean
  cueScale: number
  cueColor: string
  cueBg: number
  cueOutline: boolean
}

export const defaultPrefs: Prefs = {
  maxBitrateMbps: 0,
  directPlay: true,
  audioLang: 'auto',
  subtitleLang: 'auto',
  subtitleMode: 'auto',
  autoNext: true,
  cueScale: 1,
  cueColor: '#ffffff',
  cueBg: 0.5,
  cueOutline: true,
}

const KEY = 'nuvio.prefs'

function load(): Prefs {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return defaultPrefs
    return { ...defaultPrefs, ...(JSON.parse(raw) as Partial<Prefs>) }
  } catch {
    return defaultPrefs
  }
}

interface PrefsContextValue {
  prefs: Prefs
  update: (patch: Partial<Prefs>) => void
  reset: () => void
}

const PrefsContext = createContext<PrefsContextValue | null>(null)

export function PrefsProvider({ children }: { children: ReactNode }) {
  const [prefs, setPrefs] = useState<Prefs>(load)

  useEffect(() => {
    localStorage.setItem(KEY, JSON.stringify(prefs))
  }, [prefs])

  const value = useMemo<PrefsContextValue>(
    () => ({
      prefs,
      update: (patch) => setPrefs((p) => ({ ...p, ...patch })),
      reset: () => setPrefs(defaultPrefs),
    }),
    [prefs],
  )
  return <PrefsContext.Provider value={value}>{children}</PrefsContext.Provider>
}

export function usePrefs(): PrefsContextValue {
  const ctx = useContext(PrefsContext)
  if (!ctx) throw new Error('usePrefs must be used inside PrefsProvider')
  return ctx
}
