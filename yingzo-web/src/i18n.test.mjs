import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, Trans } from 'react-i18next'
import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY, i18n, t, message, setLocale, registerLocale, supportedLocales, resolveLocale, preferredLocale, formatDate, formatNumber } from './i18n/index.js'
import { request } from './lib.js'

const catalogs = Object.fromEntries(supportedLocales().map(({ code }) => [code, JSON.parse(readFileSync(new URL(`./i18n/locales/${code}.json`, import.meta.url), 'utf8'))]))
const placeholders = value => [...value.matchAll(/{{\s*(\w+)\s*}}/g)].map(match => match[1]).sort()
const components = value => [...value.matchAll(/<\/?\d+\s*\/?>/g)].map(match => match[0]).sort()

test('bundled languages have complete translations and preserve interpolation/component slots', () => {
  const keys = Object.keys(catalogs['zh-CN']).sort()
  for (const [locale, messages] of Object.entries(catalogs)) {
    assert.deepEqual(Object.keys(messages).sort(), keys, `${locale}: missing or extra keys`)
    for (const key of keys) {
      assert.equal(typeof messages[key], 'string', `${locale}: ${key}`)
      assert.ok(messages[key].trim(), `${locale}: empty translation for ${key}`)
      assert.deepEqual(placeholders(messages[key]), placeholders(key), `${locale}: interpolation changed in ${key}`)
      assert.deepEqual(components(messages[key]), components(key), `${locale}: rich text slots changed in ${key}`)
    }
  }
})

test('page and helper translation keys exist in every bundled catalog', () => {
  const root = new URL('./', import.meta.url)
  const files = readdirSync(root).filter(name => /\.(js|jsx)$/.test(name) && !name.includes('.test.'))
  files.push('i18n/LocaleProvider.jsx')
  for (const file of files) {
    const source = readFileSync(new URL(file, root), 'utf8')
    const keys = [...source.matchAll(/\b(?:tr|message|t)\(\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')/g)].map(match => {
      return match[1][0] === '"' ? JSON.parse(match[1]) : match[1].slice(1, -1).replace(/\\'/g, "'")
    })
    keys.push(...[...source.matchAll(/i18nKey="([^"\n]+)"/g)].map(match => match[1]))
    for (const key of keys) {
      for (const [locale, messages] of Object.entries(catalogs)) assert.ok(Object.hasOwn(messages, key), `${file}: ${locale} missing ${key}`)
    }
  }
})

test('language negotiation recognizes regional tags and honors saved preferences', () => {
  assert.equal(preferredLocale('vi', ['en-US', 'zh-CN']), 'vi')
  assert.equal(preferredLocale('unknown', ['fr-FR', 'vi-VN']), 'vi')
  assert.equal(preferredLocale(null, ['en-GB']), 'en')
  assert.equal(preferredLocale(null, ['fr']), DEFAULT_LOCALE)
  assert.equal(resolveLocale('zh_Hans_CN'), 'zh-CN')
  assert.equal(resolveLocale('VI-vn'), 'vi')
  assert.equal(resolveLocale('en-AU'), 'en')
  assert.equal(resolveLocale('not-a-locale'), null)
})

test('switching persists the preference, localizes saved notices and formats CNY numbers without changing their value', async () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const stored = new Map()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: { setItem: (key, value) => stored.set(key, value) } })
  const notice = message('{{value1}} 秒', { value1: 12 })
  try {
    await setLocale('en-US')
    assert.equal(stored.get(LOCALE_STORAGE_KEY), 'en')
    assert.equal(t('登录'), 'Sign in')
    assert.equal(t(notice), '12 seconds')
    assert.equal(formatNumber(1234.5, { minimumFractionDigits: 2 }), '1,234.50')
    await setLocale('vi')
    assert.equal(t('登录'), 'Đăng nhập')
    assert.equal(t(notice), '12 giây')
    assert.equal(formatNumber(1234.5, { minimumFractionDigits: 2 }), '1.234,50')
    assert.equal(formatDate('2026-10-30T12:00:00Z', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone: 'UTC' }), '30/10/2026')
    await assert.rejects(setLocale('invalid'), /Unsupported locale/)
  } finally {
    await setLocale(DEFAULT_LOCALE, { persist: false })
    if (original) Object.defineProperty(globalThis, 'localStorage', original)
    else delete globalThis.localStorage
  }
})

test('rich translations keep live React values and escape untrusted text', async () => {
  try {
    for (const locale of ['en', 'vi']) {
      await setLocale(locale, { persist: false })
      const html = renderToStaticMarkup(React.createElement(I18nextProvider, { i18n }, React.createElement(Trans, {
        i18nKey: '有效期至 <0/>', components: [React.createElement(React.Fragment, null, '<script>unsafe</script>')],
      })))
      assert.ok(html.includes('&lt;script&gt;unsafe&lt;/script&gt;'))
      assert.ok(!html.includes('<script>'))
      assert.ok(!html.includes('<0'))
      assert.ok(!html.includes('有效期'))
    }
  } finally { await setLocale(DEFAULT_LOCALE, { persist: false }) }
})

test('API requests include the selected language alongside custom headers', async () => {
  const originalFetch = globalThis.fetch
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  let headers
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: { getItem: () => null } })
  globalThis.fetch = async (_url, options) => {
    headers = options.headers
    return new Response(JSON.stringify({ code: 0, data: {} }), { status: 200 })
  }
  try {
    await setLocale('vi', { persist: false })
    await request('/settings/public', { headers: { 'X-Client': 'locale-test' } })
    assert.equal(headers['Accept-Language'], 'vi')
    assert.equal(headers['X-Client'], 'locale-test')
    assert.equal(headers['Content-Type'], 'application/json')
  } finally {
    globalThis.fetch = originalFetch
    if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
    else delete globalThis.localStorage
    await setLocale(DEFAULT_LOCALE, { persist: false })
  }
})

test('new languages register without changing page code and missing strings fall back to Chinese', async () => {
  const code = 'fr'
  try {
    registerLocale({ code, name: 'Français', messages: { 登录: 'Connexion' }, aliases: ['fr-FR'] })
    assert.ok(supportedLocales().some(locale => locale.code === code && locale.name === 'Français'))
    await setLocale('fr-FR', { persist: false })
    assert.equal(t('登录'), 'Connexion')
    assert.equal(t('创建账户'), '创建账户')
  } finally { await setLocale(DEFAULT_LOCALE, { persist: false }) }
})
