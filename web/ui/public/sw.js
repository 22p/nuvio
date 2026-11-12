// Nuvio app shell service worker.
//
// Only the client bundle is cached. API responses, media streams, subtitles
// and artwork stay network-only so the cache never holds another account's
// data or a stale playback decision.
const CACHE = 'nuvio-shell-v1'
const SHELL = '/index.html'
const STATIC = [
  '/favicon.svg',
  '/manifest.webmanifest',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  '/icons/icon-maskable-512.png',
  '/apple-touch-icon.png',
]
const STATIC_PATHS = new Set(STATIC)

self.addEventListener('install', (event) => {
  event.waitUntil(
    precache()
      .catch(() => {})
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys()
      await Promise.all(names.filter((name) => name !== CACHE).map((name) => caches.delete(name)))
      await self.clients.claim()
    })(),
  )
})

// Cache the HTML entry point and every hashed asset it references, so an
// offline reload still boots the client instead of a browser error page.
async function precache() {
  const cache = await caches.open(CACHE)
  const urls = new Set(STATIC)
  let html = ''

  try {
    const response = await fetch(SHELL, { cache: 'reload' })
    if (response.ok) {
      html = await response.text()
      await cache.put(SHELL, htmlResponse(html))
    }
  } catch {
    // Offline install: keep whatever the previous worker cached.
  }

  for (const match of html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)) {
    urls.add(match[1])
  }

  await Promise.all(
    [...urls].map(async (url) => {
      if (await cache.match(url)) return
      try {
        const response = await fetch(url, { cache: 'reload' })
        if (response.ok) await cache.put(url, response)
      } catch {
        // A missing optional asset must not fail the install.
      }
    }),
  )
}

self.addEventListener('fetch', (event) => {
  const request = event.request
  if (request.method !== 'GET') return

  let url
  try {
    url = new URL(request.url)
  } catch {
    return
  }
  if (url.origin !== self.location.origin) return

  if (request.mode === 'navigate' || request.destination === 'document') {
    event.respondWith(navigate(request))
    return
  }

  if (url.pathname === '/manifest.webmanifest') {
    // Not content-hashed: revalidate so icon/name changes reach installed apps.
    event.respondWith(networkFirst(request))
    return
  }

  if (url.pathname.startsWith('/assets/') || STATIC_PATHS.has(url.pathname)) {
    event.respondWith(cacheFirst(request))
  }
})

// Rebuild the response instead of storing the network headers: a gzip
// Content-Encoding from a reverse proxy would not match the decoded body.
function htmlResponse(html) {
  return new Response(html, {
    status: 200,
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  })
}

// Online users always get the current shell; the cached copy is only a
// fallback for a server that cannot be reached.
async function navigate(request) {
  const cache = await caches.open(CACHE)
  try {
    const response = await fetch(request)
    if (response.ok && (response.headers.get('content-type') || '').includes('text/html')) {
      const html = await response.clone().text()
      await cache.put(SHELL, htmlResponse(html)).catch(() => {})
    }
    return response
  } catch {
    const cached = await cache.match(SHELL)
    if (cached) return cached
    return Response.error()
  }
}

// Vite assets are content hashed, so a hit is always correct.
async function cacheFirst(request) {
  const cache = await caches.open(CACHE)
  const cached = await cache.match(request)
  if (cached) return cached
  const response = await fetch(request)
  if (response.ok) await cache.put(request, response.clone()).catch(() => {})
  return response
}

// Non-hashed files such as the manifest should update when the server is
// reachable, with the cache only used as the offline fallback.
async function networkFirst(request) {
  const cache = await caches.open(CACHE)
  try {
    const response = await fetch(request)
    if (response.ok) await cache.put(request, response.clone()).catch(() => {})
    return response
  } catch {
    return (await cache.match(request)) || Response.error()
  }
}
