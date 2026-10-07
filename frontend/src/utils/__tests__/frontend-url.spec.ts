import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildFrontendUrl } from '../frontend-url'

describe('frontend document navigation', () => {
  afterEach(() => vi.unstubAllEnvs())

  it.each(['/', '/admin/'])('keeps login within the %s deployment', (base) => {
    vi.stubEnv('BASE_URL', base)
    expect(buildFrontendUrl('/login')).toBe(`${base}login`)
    expect(buildFrontendUrl('login?redirect=/admin/dashboard')).toBe(
      `${base}login?redirect=/admin/dashboard`,
    )
  })
})
