import test from 'node:test'
import assert from 'node:assert/strict'
import { request } from './lib.js'

// Public email flows must report their own failures, even when the browser has
// an old login session. Refreshing that session can clear it or hide the error.
for (const path of ['/auth/send-verify-code', '/auth/forgot-password', '/auth/reset-password']) {
  test(`${path} reports a rejection without refreshing or clearing an existing session`, async () => {
    const originalFetch = globalThis.fetch
    const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
    const values = new Map([['auth_token', 'existing-access'], ['refresh_token', 'existing-refresh']])
    const calls = []
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: {
        getItem: key => values.get(key) ?? null,
        setItem: (key, value) => values.set(key, value),
        removeItem: key => values.delete(key),
      },
    })
    globalThis.fetch = async (url, options) => {
      calls.push({ url, options })
      return new Response(JSON.stringify({ code: 401, reason: 'EMAIL_FLOW_REJECTED', message: 'Email request rejected' }), {
        status: 401, headers: { 'Content-Type': 'application/json' },
      })
    }
    try {
      await assert.rejects(request(path, { method: 'POST', body: JSON.stringify({ email: 'user@example.com' }) }), error => {
        assert.equal(error.status, 401)
        assert.equal(error.reason, 'EMAIL_FLOW_REJECTED')
        assert.equal(error.message, 'Email request rejected')
        return true
      })
      assert.equal(calls.length, 1, 'an email flow rejection must not trigger /auth/refresh')
      assert.equal(calls[0].url, `/api/v1${path}`)
      assert.equal(values.get('auth_token'), 'existing-access')
      assert.equal(values.get('refresh_token'), 'existing-refresh')
    } finally {
      globalThis.fetch = originalFetch
      if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
      else delete globalThis.localStorage
    }
  })
}
