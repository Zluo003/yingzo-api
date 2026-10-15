import test from 'node:test'
import assert from 'node:assert/strict'
import { hasAuthTokens, pageFromPath, requiresLogin, restoreAuthSession } from './auth-session.js'
import { AUTH_EXPIRED_EVENT, clearAuthKeys, refreshAuthTokens, request } from './lib.js'

function mockSession(t, initial, respond) {
  const originals = Object.fromEntries(['fetch', 'localStorage', 'window'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  const values = new Map(Object.entries(initial))
  const events = []
  const calls = []
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  } })
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { dispatchEvent: event => events.push(event.type) } })
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options })
    const { status = 200, data = {} } = await respond(url, options)
    return new Response(JSON.stringify(status === 200 ? { code: 0, data } : { code: status, message: 'Rejected' }), {
      status, headers: { 'Content-Type': 'application/json' },
    })
  }
  t.after(() => {
    for (const [key, descriptor] of Object.entries(originals)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
  })
  return { values, calls, events }
}

test('all console routes and new routes require login, including trailing slashes', () => {
  for (const path of ['/keys', '/models', '/usage', '/recharge', '/profile', '/payment/stripe', '/future-console-page', '/keys/']) {
    assert.equal(requiresLogin(pageFromPath(path)), true, path)
  }
  for (const path of ['/', '/download', '/login', '/register', '/forgot-password', '/reset-password', '/payment/result', '/recharge/result', '/payment/airwallex']) {
    assert.equal(requiresLogin(pageFromPath(path)), false, path)
  }
})

test('a cached profile without tokens cannot restore an authenticated session', async t => {
  const { values, calls } = mockSession(t, { auth_user: JSON.stringify({ id: 42 }) }, () => assert.fail('must not fetch'))
  assert.equal(hasAuthTokens(), false)
  assert.equal(await restoreAuthSession(), null)
  assert.equal(values.has('auth_user'), false)
  assert.equal(calls.length, 0)
})

test('restoration confirms identity with the server instead of the cached profile', async t => {
  const { values, calls } = mockSession(t, { auth_token: 'access', auth_user: JSON.stringify({ id: 99 }) }, () => ({ data: { id: 42 } }))
  assert.deepEqual(await restoreAuthSession(), { id: 42 })
  assert.equal(calls[0].url, '/api/v1/auth/me')
  assert.equal(calls[0].options.headers.Authorization, 'Bearer access')
  // The mounted session hook persists only results from the current check,
  // preventing late requests from overwriting a newer login or logout.
  assert.deepEqual(JSON.parse(values.get('auth_user')), { id: 99 })
})

test('refresh-only sessions rotate tokens before verifying the user', async t => {
  const { calls, values } = mockSession(t, { refresh_token: 'refresh' }, url => url.endsWith('/refresh')
    ? { data: { access_token: 'new-access', refresh_token: 'new-refresh' } }
    : { data: { user: { id: 42 } } })
  assert.deepEqual(await restoreAuthSession(), { id: 42 })
  assert.deepEqual(calls.map(call => call.url), ['/api/v1/auth/refresh', '/api/v1/auth/me'])
  assert.equal(values.get('refresh_token'), 'new-refresh')
})

test('a definitive 401 without refresh credentials clears the session and updates the UI', async t => {
  const { values, events } = mockSession(t, { auth_token: 'expired', auth_user: '{"id":42}' }, () => ({ status: 401 }))
  await assert.rejects(request('/keys'), { status: 401 })
  assert.equal(values.size, 0)
  assert.ok(events.includes(AUTH_EXPIRED_EVENT))
})

test('a rejected fresh access token cannot leave an authenticated console visible', async t => {
  const { values, events, calls } = mockSession(t, { auth_token: 'expired', refresh_token: 'refresh' }, url => url.endsWith('/refresh')
    ? { data: { access_token: 'new-access', refresh_token: 'new-refresh' } }
    : { status: 401 })
  await assert.rejects(request('/auth/me'), { status: 401 })
  assert.equal(calls.length, 3)
  assert.equal(values.size, 0)
  assert.ok(events.includes(AUTH_EXPIRED_EVENT))
})

for (const status of [429, 503]) {
  test(`refresh ${status} preserves tokens and reports a recoverable verification error`, async t => {
    const { values, events } = mockSession(t, { auth_token: 'expired', refresh_token: 'refresh' }, url => ({ status: url.endsWith('/refresh') ? status : 401 }))
    await assert.rejects(restoreAuthSession(), error => error.sessionUnavailable === true)
    assert.equal(values.get('refresh_token'), 'refresh')
    assert.equal(values.get('auth_token'), 'expired')
    assert.equal(events.length, 0)
  })
}

test('a refresh response arriving after logout cannot restore the old session', async t => {
  let finish
  mockSession(t, { refresh_token: 'refresh' }, () => new Promise(resolve => { finish = resolve }))
  const pending = refreshAuthTokens()
  clearAuthKeys()
  finish({ data: { access_token: 'late-access', refresh_token: 'late-refresh' } })
  await assert.rejects(pending, { status: 401 })
  assert.equal(hasAuthTokens(), false)
})
