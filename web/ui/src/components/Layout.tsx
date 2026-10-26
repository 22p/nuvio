import { t } from '../lib/i18n'
import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { createContext, useContext } from 'react'
import { getScanStatus, getViews } from '../lib/api'
import { initials } from '../lib/format'
import type { JFItem } from '../lib/types'
import { useAuth } from '../lib/auth'
import {
  IconGrid,
  IconHome,
  IconLogout,
  IconSearch,
  IconSettings,
  IconUser,
  IconUsers,
} from './Icons'

const ViewsContext = createContext<JFItem[]>([])
export const useViews = () => useContext(ViewsContext)

export function Logo({ size = 30 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" className="logo-mark">
      <defs>
        <linearGradient id="nuvio-mark" x1="0" y1="0" x2="1" y2="1">
          <stop stopColor="#b8a1ff" />
          <stop offset="0.55" stopColor="#8b5cf6" />
          <stop offset="1" stopColor="#22d3ee" />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="10" fill="url(#nuvio-mark)" />
      <path
        d="M10 22.5V9.5l12 13V9.5"
        fill="none"
        stroke="#07080c"
        strokeWidth="3.1"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

export function AppShell() {
  const { session } = useAuth()
  const location = useLocation()
  const [views, setViews] = useState<JFItem[]>([])

  useEffect(() => {
    if (!session) return
    let alive = true
    let timer = 0
    let attempts = 0
    const load = async () => {
      try {
        const res = await getViews(session.user.Id)
        if (!alive) return
        setViews(res.Items || [])
        const status = await getScanStatus().catch(() => null)
        if (status?.scanning) {
          attempts = 0
          timer = window.setTimeout(load, 2500)
        } else if (!res.Items?.length && attempts++ < 3) {
          timer = window.setTimeout(load, 2500)
        }
      } catch {
        if (alive && attempts++ < 20) timer = window.setTimeout(load, 4000)
      }
    }
    load()
    return () => {
      alive = false
      window.clearTimeout(timer)
    }
  }, [session, location.pathname])

  return (
    <ViewsContext.Provider value={views}>
      <div className="app-shell">
        <Header views={views} />
        <main className="page">
          <Outlet />
        </main>
        <MobileNav />
      </div>
    </ViewsContext.Provider>
  )
}

function Header({ views }: { views: JFItem[] }) {
  const { session, signOut, isAdmin } = useAuth()
  const navigate = useNavigate()
  const [scrolled, setScrolled] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 12)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    if (!menuOpen) return
    const onDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [menuOpen])

  const movies = views.find((v) => v.CollectionType === 'movies')
  const shows = views.find((v) => v.CollectionType === 'tvshows')
  const name = session?.user.Name || 'N'

  return (
    <header className={`site-header ${scrolled ? 'is-scrolled' : ''}`}>
      <div className="header-inner">
        <Link to="/" className="brand" aria-label={t("Nuvio home")}>
          <Logo size={30} />
          <span className="brand-name">NUVIO</span>
        </Link>

        <nav className="desktop-nav">
          <NavLink to="/" end>{t("Home")}</NavLink>
          <NavLink to="/browse">{t("Library")}</NavLink>
          {movies && <NavLink to={`/library/${movies.Id}`}>{t("Movies")}</NavLink>}
          {shows && <NavLink to={`/library/${shows.Id}`}>{t("Series")}</NavLink>}
          <NavLink to="/search">{t("Search")}</NavLink>
        </nav>

        <div className="header-actions">
          <button className="icon-button" onClick={() => navigate('/search')} aria-label={t("Search")}>
            <IconSearch size={19} />
          </button>
          <div className="avatar-wrap" ref={menuRef}>
            <button
              className="avatar"
              onClick={() => setMenuOpen((v) => !v)}
              aria-label={t("Account menu")}
            >
              {initials(name)}
            </button>
            {menuOpen && (
              <div className="avatar-menu">
                <div className="avatar-menu-head">
                  <div className="avatar avatar-sm">{initials(name)}</div>
                  <div>
                    <div className="avatar-name">{name}</div>
                    <div className="avatar-server">{session?.serverName}</div>
                  </div>
                </div>
                <button className="menu-item" onClick={() => { setMenuOpen(false); navigate('/settings') }}>
                  <IconSettings size={18} />{' '}{t("Settings")}</button>
                <button className="menu-item" onClick={() => { setMenuOpen(false); navigate('/settings#account') }}>
                  <IconUser size={18} />{' '}{t("Account")}</button>
                {isAdmin && (
                  <button className="menu-item" onClick={() => { setMenuOpen(false); navigate('/users') }}>
                    <IconUsers size={18} />{' '}{t("Users")}</button>
                )}
                <button className="menu-item danger" onClick={() => signOut()}>
                  <IconLogout size={18} />{' '}{t("Sign out")}</button>
              </div>
            )}
          </div>
        </div>
      </div>
    </header>
  )
}

function MobileNav() {
  const items = [
    { to: '/', label: t("Home"), icon: <IconHome size={21} />, end: true },
    { to: '/browse', label: t("Library"), icon: <IconGrid size={21} /> },
    { to: '/search', label: t("Search"), icon: <IconSearch size={21} /> },
    { to: '/settings', label: t("Profile"), icon: <IconSettings size={21} /> },
  ]
  return (
    <nav className="mobile-nav">
      {items.map((item) => (
        <NavLink key={item.to} to={item.to} end={item.end}>
          {item.icon}
          <span>{item.label}</span>
        </NavLink>
      ))}
    </nav>
  )
}
