interface IconProps {
  size?: number
  className?: string
}

const base = (size: number) => ({
  width: size,
  height: size,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
})

export function IconSearch({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </svg>
  )
}

export function IconPlay({ size = 20, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" className={className}>
      <path d="M8 5.14v13.72a1 1 0 0 0 1.52.85l11.02-6.86a1 1 0 0 0 0-1.7L9.52 4.29A1 1 0 0 0 8 5.14Z" />
    </svg>
  )
}

export function IconPause({ size = 20, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" className={className}>
      <rect x="6" y="4.5" width="4" height="15" rx="1.2" />
      <rect x="14" y="4.5" width="4" height="15" rx="1.2" />
    </svg>
  )
}

export function IconPlus({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

export function IconCheck({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m4.5 12.5 5 5L20 6.5" />
    </svg>
  )
}

export function IconStar({ size = 16, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" className={className}>
      <path d="m12 2.6 2.9 5.9 6.5.95-4.7 4.58 1.1 6.47L12 17.44 6.2 20.5l1.1-6.47L2.6 9.45l6.5-.95Z" />
    </svg>
  )
}

export function IconChevronLeft({ size = 24, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m15 5-7 7 7 7" />
    </svg>
  )
}

export function IconChevronRight({ size = 24, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m9 5 7 7-7 7" />
    </svg>
  )
}

export function IconBack({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M19 12H5m6-7-7 7 7 7" />
    </svg>
  )
}

export function IconVolume({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M11 5 6.5 9H3v6h3.5L11 19V5Z" fill="currentColor" stroke="none" />
      <path d="M15 9a4 4 0 0 1 0 6M17.5 6.5a8 8 0 0 1 0 11" />
    </svg>
  )
}

export function IconMute({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M11 5 6.5 9H3v6h3.5L11 19V5Z" fill="currentColor" stroke="none" />
      <path d="m16 10 4 4m0-4-4 4" />
    </svg>
  )
}

export function IconSubtitles({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <rect x="3" y="5" width="18" height="14" rx="3" />
      <path d="M7 13h4M13 13h4M7 16.5h7" />
    </svg>
  )
}

export function IconAudio({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M4 10v4M8 7v10M12 4v16M16 8v8M20 10v4" />
    </svg>
  )
}

export function IconSettings({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <circle cx="12" cy="12" r="3.2" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06-1.9 1.9-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V20h-2.7v-.08a1.7 1.7 0 0 0-1.1-1.56 1.7 1.7 0 0 0-1.8.34l-.06.06-1.9-1.9.06-.06A1.7 1.7 0 0 0 8.7 15a1.7 1.7 0 0 0-1.56-1.03H7v-2.7h.08A1.7 1.7 0 0 0 8.64 10a1.7 1.7 0 0 0-.34-1.8l-.06-.06 1.9-1.9.06.06a1.7 1.7 0 0 0 1.87.34A1.7 1.7 0 0 0 13.1 5.1V5h2.7v.08a1.7 1.7 0 0 0 1.03 1.56 1.7 1.7 0 0 0 1.87-.34l.06-.06 1.9 1.9-.06.06a1.7 1.7 0 0 0-.34 1.87A1.7 1.7 0 0 0 21.8 11H22v2.7h-.08A1.7 1.7 0 0 0 20.4 15Z" />
    </svg>
  )
}

export function IconFullscreen({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M8 3H3v5M16 3h5v5M21 16v5h-5M3 16v5h5" />
    </svg>
  )
}

export function IconExitFullscreen({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M3 8h5V3M21 8h-5V3M16 21v-5h5M8 21v-5H3" />
    </svg>
  )
}

export function IconPip({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <rect x="12" y="11" width="7" height="6" rx="1.2" fill="currentColor" stroke="none" />
    </svg>
  )
}

export function IconNext({ size = 22, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" className={className}>
      <path d="M5 5.5v13a1 1 0 0 0 1.54.84l9.2-6.5a1 1 0 0 0 0-1.68l-9.2-6.5A1 1 0 0 0 5 5.5Z" />
      <rect x="18" y="5" width="2.4" height="14" rx="1" />
    </svg>
  )
}

export function IconHome({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m3.5 10.5 8.5-7 8.5 7V20a1 1 0 0 1-1 1h-5v-6h-5v6h-5a1 1 0 0 1-1-1Z" />
    </svg>
  )
}

export function IconGrid({ size = 22, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <rect x="3" y="3" width="7.5" height="7.5" rx="2" />
      <rect x="13.5" y="3" width="7.5" height="7.5" rx="2" />
      <rect x="3" y="13.5" width="7.5" height="7.5" rx="2" />
      <rect x="13.5" y="13.5" width="7.5" height="7.5" rx="2" />
    </svg>
  )
}

export function IconInfo({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v6M12 7.5v.2" />
    </svg>
  )
}

export function IconClose({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m6 6 12 12M18 6 6 18" />
    </svg>
  )
}

export function IconTrash({ size = 18, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M4 7h16M9 7V4.5h6V7M6.5 7l.8 12.2A1.5 1.5 0 0 0 8.8 20.5h6.4a1.5 1.5 0 0 0 1.5-1.3L17.5 7" />
    </svg>
  )
}

export function IconRefresh({ size = 18, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="M20 11a8 8 0 1 0-2.3 6.3M20 4v7h-7" />
    </svg>
  )
}

export function IconLayers({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <path d="m12 3 9 5-9 5-9-5 9-5Z" />
      <path d="m3 13 9 5 9-5M3 17l9 5 9-5" />
    </svg>
  )
}

export function IconUser({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21c.8-4 4-6 8-6s7.2 2 8 6" />
    </svg>
  )
}

export function IconUsers({ size = 20, className }: IconProps) {
  return (
    <svg {...base(size)} className={className}>
      <circle cx="9" cy="8" r="3.4" />
      <path d="M2.5 20c.7-3.4 3.4-5.2 6.5-5.2s5.8 1.8 6.5 5.2" />
      <path d="M16 5.2a3.4 3.4 0 0 1 0 6.6M18.5 14.9c2 .7 3.2 2.4 3.6 5.1" />
    </svg>
  )
}

export function IconLogout({ size = 18, className }: IconProps) {  return (
    <svg {...base(size)} className={className}>
      <path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 8l-4 4 4 4M6 12h9" />
    </svg>
  )
}
