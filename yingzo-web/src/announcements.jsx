// Announcement system — same backend contract and behavior as the original frontend
// (frontend/src/stores/announcements.ts + AnnouncementBell/AnnouncementPopup), restyled
// for the yingzo-web design language.
import { useState, useSyncExternalStore } from 'react'
import { request, renderMarkdown, relativeTime } from './lib.js'

const THROTTLE_MS = 20 * 60 * 1000
const MAX_LIST = 20
const POPUP_GAP_MS = 300

let state = {
  announcements: [],
  loading: false,
  currentPopup: null,
  listOpen: false,
  detail: null,
}
let lastFetchTime = 0
let popupQueue = []
const shownPopupIds = new Set()
const listeners = new Set()

function emit(patch) {
  state = { ...state, ...patch }
  listeners.forEach(l => l())
}
function subscribe(listener) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
function getSnapshot() { return state }

export function useAnnouncements() {
  return useSyncExternalStore(subscribe, getSnapshot)
}

export function unreadCount() { return state.announcements.filter(a => !a.read_at).length }

export async function fetchAnnouncements(force = false) {
  const now = Date.now()
  if (!force && now - lastFetchTime < THROTTLE_MS) return
  lastFetchTime = now // set before the request so concurrent callers are throttled too
  emit({ loading: true })
  try {
    const all = await request('/announcements')
    const announcements = (Array.isArray(all) ? all : []).slice(0, MAX_LIST)
    emit({ announcements })
    enqueueNewPopups(announcements)
  } catch {
    lastFetchTime = 0 // allow an immediate retry after a failure
  } finally {
    emit({ loading: false })
  }
}

function enqueueNewPopups(list) {
  const seen = new Set(popupQueue)
  list.forEach(a => {
    if (a.notify_mode === 'popup' && !a.read_at && !shownPopupIds.has(a.id) && !seen.has(a.id)) {
      popupQueue.push(a)
      seen.add(a.id)
    }
  })
  if (!state.currentPopup && popupQueue.length) showNextPopup()
}

function showNextPopup() {
  const next = popupQueue.shift()
  if (next) {
    shownPopupIds.add(next.id)
    emit({ currentPopup: next })
  }
}

export function dismissPopup() {
  const current = state.currentPopup
  emit({ currentPopup: null })
  if (current && !current.read_at) {
    markAsRead(current.id).catch(() => {})
  }
  if (popupQueue.length) setTimeout(showNextPopup, POPUP_GAP_MS)
}

export async function markAsRead(id) {
  await request(`/announcements/${id}/read`, { method: 'POST' })
  emit({
    announcements: state.announcements.map(a => a.id === id && !a.read_at ? { ...a, read_at: new Date().toISOString() } : a),
    detail: state.detail && state.detail.id === id && !state.detail.read_at ? { ...state.detail, read_at: new Date().toISOString() } : state.detail,
  })
}

export async function markAllAsRead() {
  const unread = state.announcements.filter(a => !a.read_at)
  await Promise.all(unread.map(a => markAsRead(a.id)))
}

export function openList() { emit({ listOpen: true, detail: null }) }
export function closeList() { emit({ listOpen: false, detail: null }) }
export function openDetail(announcement) {
  emit({ detail: announcement })
  if (!announcement.read_at) markAsRead(announcement.id).catch(() => {})
}
export function closeDetail() { emit({ detail: null }) }

export function resetAnnouncements() {
  popupQueue = []
  shownPopupIds.clear()
  lastFetchTime = 0
  state = { announcements: [], loading: false, currentPopup: null, listOpen: false, detail: null }
  listeners.forEach(l => l())
}

// Trigger wiring (mirrors App.vue) lives in App.jsx: force fetch 3s after a fresh
// login, throttled fetch on session restore / tab re-visibility / route change.

function BellIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 22a2.3 2.3 0 0 0 2.29-2.3H9.7A2.3 2.3 0 0 0 12 22Zm7-5.3v-1l-1.5-1.5v-4.4A5.7 5.7 0 0 0 13.6 4V3.4a1.6 1.6 0 1 0-3.2 0V4a5.7 5.7 0 0 0-3.9 5.8v4.4L5 15.7v1Z"/></svg>
}

function Markdown({ text, className = '' }) {
  return <div className={`md-body ${className}`} dangerouslySetInnerHTML={{ __html: renderMarkdown(text) }}/>
}

export function AnnouncementBell() {
  const s = useAnnouncements()
  const unread = s.announcements.filter(a => !a.read_at).length
  return <>
    <button className="bell-button" title="公告" aria-label="公告" onClick={openList}>
      <BellIcon/>
      {unread > 0 && <i className="bell-dot"/>}
    </button>
    {s.listOpen && <AnnouncementListModal unread={unread}/>}
    {s.detail && <AnnouncementDetailModal/>}
  </>
}

function AnnouncementListModal({ unread }) {
  const s = useAnnouncements()
  const [marking, setMarking] = useState(false)
  async function markAll() {
    setMarking(true)
    try { await markAllAsRead() } catch { /* keep list open on failure */ } finally { setMarking(false) }
  }
  return <div className="modal-backdrop ann-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) closeList() }}>
    <div className="ann-list-card">
      <header className="ann-list-head">
        <div>
          <p className="kicker">YINGZO / 公告</p>
          <h2>{unread > 0 ? `${unread} 条未读公告` : '公告'}</h2>
        </div>
        <div className="ann-list-actions">
          {unread > 0 && <button className="outline" disabled={marking} onClick={markAll}>{marking ? '处理中…' : '全部已读'}</button>}
          <button className="icon-button" onClick={closeList}>关闭</button>
        </div>
      </header>
      <div className="ann-list-body">
        {s.announcements.length
          ? s.announcements.map(a => {
            const isUnread = !a.read_at
            return <button className={`ann-item ${isUnread ? 'unread' : ''}`} key={a.id} onClick={() => openDetail(a)}>
              <span className="ann-item-mark">{isUnread ? '●' : '○'}</span>
              <span className="ann-item-copy">
                <span className="ann-item-title">{a.title}{isUnread && <em className="ann-badge">未读</em>}</span>
                <span className="ann-item-time">{relativeTime(a.created_at)}{a.notify_mode === 'popup' ? ' · 弹窗通知' : ''}</span>
              </span>
              <span className="ann-item-arrow">→</span>
            </button>
          })
          : <div className="empty"><strong>暂无公告</strong><p>新的平台通知会出现在这里。</p></div>}
      </div>
    </div>
  </div>
}

function AnnouncementDetailModal() {
  const s = useAnnouncements()
  const a = s.detail
  const isUnread = a && !a.read_at
  return <div className="modal-backdrop ann-backdrop ann-detail-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) closeDetail() }}>
    <div className="ann-detail-card">
      <p className="kicker">YINGZO / 公告</p>
      <h2>{a.title}</h2>
      <p className="ann-detail-meta">{relativeTime(a.created_at)} · {isUnread ? '未读' : '已读'}</p>
      <Markdown text={a.content}/>
      <div className="modal-actions">
        <button className="dark" onClick={closeDetail}>关闭</button>
      </div>
    </div>
  </div>
}

export function AnnouncementPopup() {
  const s = useAnnouncements()
  const a = s.currentPopup
  if (!a) return null
  return <div className="modal-backdrop ann-backdrop ann-popup-backdrop">
    <div className="ann-popup-card">
      <p className="kicker">YINGZO / 公告</p>
      <h2>{a.title}</h2>
      <p className="ann-detail-meta"><span className="ann-badge">未读</span> {relativeTime(a.created_at)}</p>
      <Markdown text={a.content}/>
      <div className="modal-actions">
        <button className="dark" onClick={dismissPopup}>标记已读</button>
      </div>
    </div>
  </div>
}
