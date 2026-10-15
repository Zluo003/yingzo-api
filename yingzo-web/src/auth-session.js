import { clearAuthKeys, refreshAuthTokens, request } from './lib.js'

// Public payment return/recovery pages use scoped order tokens, not an account
// session. Everything else defaults to requiring login, including new routes.
const PUBLIC_PAGES = new Set([
  'home', 'download', 'login', 'register', 'forgot-password', 'reset-password',
  'payment/result', 'recharge/result', 'payment/airwallex',
])

export function pageFromPath(pathname) {
  return pathname.replace(/^\/+|\/+$/g, '') || 'home'
}

export function requiresLogin(page) {
  return !PUBLIC_PAGES.has(page)
}

export function hasAuthTokens() {
  return Boolean(localStorage.getItem('auth_token') || localStorage.getItem('refresh_token'))
}

export async function restoreAuthSession() {
  if (!hasAuthTokens()) {
    clearAuthKeys()
    return null
  }
  if (!localStorage.getItem('auth_token')) await refreshAuthTokens()
  // Cached profile data is for display only. The server confirms the session
  // before any protected page mounts or starts its own requests.
  const result = await request('/auth/me')
  const user = result?.user || result
  if (!user?.id || !hasAuthTokens()) throw new Error('无法确认登录状态，请重试。')
  return user
}
