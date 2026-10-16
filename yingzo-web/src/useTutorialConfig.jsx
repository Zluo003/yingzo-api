import { useEffect, useState } from 'react'
import { request } from './lib.js'

// Share one configuration between navigation and the reader. Until the server
// confirms publication, the tutorial entry and its content stay hidden.
export function useTutorialConfig(page) {
  const [state, setState] = useState({ config: null, page: null, loading: true })
  useEffect(() => {
    let disposed = false
    let controller
    let revision = 0
    async function refresh() {
      const current = ++revision
      controller?.abort()
      controller = new AbortController()
      try {
        const config = await request('/settings/tutorials', { signal: controller.signal })
        if (!disposed && current === revision) setState({ config, page, loading: false })
      } catch {
        if (!disposed && current === revision) setState({ config: null, page, loading: false })
      }
    }
    const onVisible = () => { if (document.visibilityState === 'visible') refresh() }
    refresh()
    addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
    // Also withdraw an already-open reader after the administrator disables it.
    const timer = setInterval(onVisible, 60000)
    return () => {
      disposed = true
      controller?.abort()
      clearInterval(timer)
      removeEventListener('focus', onVisible)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [page])
  return { config: state.config, loading: state.loading || state.page !== page }
}
