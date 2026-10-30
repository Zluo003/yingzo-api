import { createInstance } from 'i18next'
import { initReactI18next } from 'react-i18next'
import zh from './locales/zh-CN.json' with { type: 'json' }
import en from './locales/en.json' with { type: 'json' }
import vi from './locales/vi.json' with { type: 'json' }

export const DEFAULT_LOCALE = 'zh-CN'
export const LOCALE_STORAGE_KEY = 'yingzo_locale'

// Add a bundled locale here, or call registerLocale for an optional language pack.
// Chinese source messages are stable gettext-style keys. Values may contain
// i18next interpolation ({{count}}) or React Trans component slots (<0>…</0>).
const registry = new Map([
  ['zh-CN', { code: 'zh-CN', name: '简体中文', intlLocale: 'zh-CN', direction: 'ltr', aliases: ['zh', 'zh-Hans'], messages: zh }],
  ['en', { code: 'en', name: 'English', intlLocale: 'en-US', direction: 'ltr', aliases: ['en-US', 'en-GB'], messages: en }],
  ['vi', { code: 'vi', name: 'Tiếng Việt', intlLocale: 'vi-VN', direction: 'ltr', aliases: ['vi-VN'], messages: vi }],
])

export function supportedLocales() {
  return [...registry.values()].map(({ messages, ...locale }) => locale)
}

export function resolveLocale(value) {
  if (typeof value !== 'string') return null
  const tag = value.replaceAll('_', '-').toLowerCase()
  const exact = [...registry.values()].find(locale => [locale.code, ...(locale.aliases || [])].some(alias => alias.toLowerCase() === tag))
  if (exact) return exact.code
  return [...registry.values()].find(locale => locale.code.toLowerCase().split('-')[0] === tag.split('-')[0])?.code || null
}

export function preferredLocale(saved, languages = []) {
  return resolveLocale(saved) || languages.map(resolveLocale).find(Boolean) || DEFAULT_LOCALE
}

function initialLocale() {
  if (typeof window === 'undefined') return DEFAULT_LOCALE
  let saved
  try { saved = localStorage.getItem(LOCALE_STORAGE_KEY) } catch { /* Private browsing can disable storage. */ }
  return preferredLocale(saved, typeof navigator !== 'undefined' ? navigator.languages || [navigator.language] : [])
}

export const i18n = createInstance()
i18n.use(initReactI18next).init({
  lng: initialLocale(),
  fallbackLng: DEFAULT_LOCALE,
  resources: Object.fromEntries([...registry.values()].map(locale => [locale.code, { translation: locale.messages }])),
  initAsync: false,
  keySeparator: false,
  nsSeparator: false,
  returnNull: false,
  interpolation: { escapeValue: false }, // React escapes interpolated text.
  react: { useSuspense: false, bindI18n: 'languageChanged localesChanged' },
})

export function t(key, values) {
  if (key && typeof key === 'object' && typeof key.i18nKey === 'string') return i18n.t(key.i18nKey, key.values)
  if (key == null) return ''
  return i18n.t(key, values)
}
// Store UI notices as a key plus values so switching languages also updates
// existing validation messages and toasts without losing form state.
export function message(i18nKey, values) { return { i18nKey, values } }
export function getLocale() { return resolveLocale(i18n.language) || DEFAULT_LOCALE }
export function getIntlLocale() { return registry.get(getLocale())?.intlLocale || DEFAULT_LOCALE }

function syncDocumentLocale() {
  if (typeof document === 'undefined') return
  const locale = registry.get(getLocale())
  document.documentElement.lang = locale?.code || DEFAULT_LOCALE
  document.documentElement.dir = locale?.direction || 'ltr'
}
i18n.on('languageChanged', syncDocumentLocale)
syncDocumentLocale()

export async function setLocale(code, { persist = true } = {}) {
  const resolved = resolveLocale(code)
  if (!resolved) throw new Error(`Unsupported locale: ${code}`)
  await i18n.changeLanguage(resolved)
  if (persist) {
    try { localStorage.setItem(LOCALE_STORAGE_KEY, resolved) } catch { /* The current tab still switches when storage is unavailable. */ }
  }
  syncDocumentLocale()
}

// Language packs can be registered without changing the switcher or page code.
// Missing strings automatically fall back to the bundled Chinese catalog.
export function registerLocale({ code, name, messages, intlLocale = code, direction = 'ltr', aliases = [] }) {
  if (!code || !name || !messages || typeof messages !== 'object' || Array.isArray(messages)) throw new TypeError('A language pack requires code, name and a messages object')
  new Intl.DateTimeFormat(intlLocale) // Validate the formatting locale early.
  registry.set(code, { code, name, messages, intlLocale, direction, aliases })
  i18n.addResourceBundle(code, 'translation', messages, true, true)
  i18n.emit('localesChanged')
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('storage', event => {
    if (event.key === LOCALE_STORAGE_KEY) setLocale(preferredLocale(event.newValue, navigator.languages), { persist: false })
  })
}

export function formatNumber(value, options = {}) {
  return new Intl.NumberFormat(getIntlLocale(), options).format(Number(value) || 0)
}
export function formatDate(value, options = {}) {
  const date = value instanceof Date ? value : new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat(getIntlLocale(), options).format(date)
}
