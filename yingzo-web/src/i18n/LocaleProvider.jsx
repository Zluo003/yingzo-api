import { useEffect, useId, useRef, useState } from 'react'
import { I18nextProvider, useTranslation } from 'react-i18next'
import { i18n, t, getLocale, setLocale, supportedLocales } from './index.js'
import './locale.css'

// Force only a render, never remount the application: language changes must
// preserve auth, payment polling, unsaved forms and the current route.
function LocalizedApp({ component: Component }) {
  const { i18n: instance } = useTranslation()
  return <Component locale={instance.language}/>
}

export function LocaleProvider({ component }) {
  return <I18nextProvider i18n={i18n}><LocalizedApp component={component}/></I18nextProvider>
}

export function LanguageSwitcher({ className = '' }) {
  useTranslation()
  const [open, setOpen] = useState(false)
  const root = useRef(null)
  const trigger = useRef(null)
  const menuId = useId()
  const locales = supportedLocales()
  const selected = locales.findIndex(locale => locale.code === getLocale())
  function focusItem(index) {
    root.current?.querySelectorAll('[role="menuitemradio"]')[index]?.focus()
  }
  useEffect(() => {
    if (!open) return
    focusItem(Math.max(0, selected))
    const outside = event => { if (!root.current?.contains(event.target)) setOpen(false) }
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  function close() { setOpen(false); trigger.current?.focus() }
  function menuKey(event) {
    const items = [...root.current.querySelectorAll('[role="menuitemradio"]')]
    const index = items.indexOf(document.activeElement)
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() }
    else if (event.key === 'Tab') setOpen(false)
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault()
      focusItem(event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length)
    }
  }
  return <div className={`language-switcher ${className}`} ref={root} onBlur={event => {
    if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false)
  }}>
    <button className="language-trigger" type="button" ref={trigger} title={t('界面语言')} aria-label={t('界面语言')} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuId : undefined} onClick={() => setOpen(value => !value)} onKeyDown={event => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setOpen(true) }
    }}>
      <svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c5 5 5 13 0 18-5-5-5-13 0-18Z"/></svg>
    </button>
    {open && <div id={menuId} className="language-menu" role="menu" aria-label={t('界面语言')} onKeyDown={menuKey}>
      {locales.map(locale => <button type="button" role="menuitemradio" aria-checked={locale.code === getLocale()} key={locale.code} lang={locale.code} onClick={() => { void setLocale(locale.code); close() }}>
        <span>{locale.name}</span><span className="language-check" aria-hidden="true">{locale.code === getLocale() ? '✓' : ''}</span>
      </button>)}
    </div>}
  </div>
}
