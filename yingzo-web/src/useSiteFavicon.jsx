import { useEffect } from 'react'
import { request } from './lib.js'

const DEFAULT_FAVICON = '/assets/yingzo-logo.png'

function faviconURL(value) {
  if (typeof value !== 'string') return DEFAULT_FAVICON
  const url = value.trim()
  if (!url || /[\u0000-\u001f\u007f\\]/.test(url)) return DEFAULT_FAVICON
  if (url.startsWith('/') && !url.startsWith('//')) return url
  if (/^data:image\/[a-z0-9.+-]+(?:;[^,]*)?,/i.test(url)) return url
  try {
    const parsed = new URL(url)
    if (['http:', 'https:'].includes(parsed.protocol) && !parsed.username && !parsed.password) return parsed.href
  } catch { /* Invalid configured URLs use the bundled Yingzo icon. */ }
  return DEFAULT_FAVICON
}

function applyFavicon(value) {
  const url = faviconURL(value)
  const icons = [...document.head.querySelectorAll('link[rel~="icon"]')]
  let icon = icons.find(link => link.rel === 'icon')
  if (!icon) {
    icon = document.createElement('link')
    icon.rel = 'icon'
    document.head.appendChild(icon)
  }
  // Remove legacy alternatives so the browser cannot prefer the old icon.
  icons.filter(link => link !== icon).forEach(link => link.remove())
  icon.removeAttribute('type')
  icon.removeAttribute('sizes')
  if (icon.getAttribute('href') !== url) icon.setAttribute('href', url)
}

export function useSiteFavicon() {
  useEffect(() => {
    let disposed = false
    let controller
    let inFlight = false
    // Production HTML already contains this setting, so use it before fetching.
    // Static and development deployments obtain it from the public API below.
    const initial = window.__APP_CONFIG__
    if (initial && Object.hasOwn(initial, 'site_logo')) applyFavicon(initial.site_logo)

    async function refresh() {
      if (inFlight) return
      inFlight = true
      controller = new AbortController()
      try {
        const settings = await request('/settings/public', { signal: controller.signal, cache: 'no-store' })
        if (!disposed && settings && Object.hasOwn(settings, 'site_logo')) applyFavicon(settings.site_logo)
      } catch { /* Retain the last valid icon during temporary API failures. */ }
      finally { inFlight = false }
    }
    const onVisible = () => { if (document.visibilityState === 'visible') void refresh() }
    void refresh()
    window.addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      disposed = true
      controller?.abort()
      window.removeEventListener('focus', onVisible)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [])
}
