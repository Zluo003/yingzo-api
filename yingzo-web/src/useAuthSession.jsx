import { useCallback, useEffect, useRef, useState } from 'react'
import { AUTH_EXPIRED_EVENT, clearAuthKeys } from './lib.js'
import { hasAuthTokens, restoreAuthSession } from './auth-session.js'

export function useAuthSession() {
  const [session, setSession] = useState(() => ({
    status: hasAuthTokens() ? 'checking' : 'anonymous', user: null,
  }))
  const revision = useRef(0)

  const setUser = useCallback(update => {
    revision.current += 1
    setSession(previous => {
      const user = typeof update === 'function' ? update(previous.user) : update
      return user && hasAuthTokens()
        ? { status: 'authenticated', user }
        : { status: 'anonymous', user: null }
    })
  }, [])

  const checkSession = useCallback(async () => {
    const current = ++revision.current
    setSession({ status: hasAuthTokens() ? 'checking' : 'anonymous', user: null })
    try {
      const user = await restoreAuthSession()
      if (current === revision.current) {
        if (user) localStorage.setItem('auth_user', JSON.stringify(user))
        setSession({ status: user ? 'authenticated' : 'anonymous', user })
      }
    } catch (error) {
      if (current !== revision.current) return
      if ((!error.sessionUnavailable && [400, 401, 403].includes(error.status)) || !hasAuthTokens()) {
        clearAuthKeys()
        setSession({ status: 'anonymous', user: null })
      } else {
        // Temporary outages must neither expose the console nor discard a
        // recoverable refresh token. The user can retry verification.
        setSession({ status: 'error', user: null })
      }
    }
  }, [])

  useEffect(() => {
    const expired = () => setUser(null)
    const storageChanged = event => {
      if (event.storageArea !== localStorage) return
      if (event.key === null || event.key === 'refresh_token' || event.key === 'auth_token' || event.key === 'auth_user') {
        checkSession()
      }
    }
    addEventListener(AUTH_EXPIRED_EVENT, expired)
    addEventListener('storage', storageChanged)
    checkSession()
    return () => {
      revision.current += 1
      removeEventListener(AUTH_EXPIRED_EVENT, expired)
      removeEventListener('storage', storageChanged)
    }
  }, [checkSession, setUser])

  return { ...session, setUser, retry: checkSession }
}
