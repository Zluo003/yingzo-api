// Shared API client + payment-flow helpers.
// The payment logic mirrors frontend/src/components/payment/paymentFlow.ts so both
// frontends speak the exact same backend contract.
import { marked } from 'marked'
import DOMPurify from 'dompurify'

const API = import.meta.env.VITE_API_BASE || '/api/v1'
export const UPDATE_API = 'https://updata.yingzo.art/v1/updates'

export async function request(path, options = {}) {
  const token = localStorage.getItem('auth_token')
  const res = await fetch(`${API}${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(options.headers || {}) },
    ...options,
  })
  const raw = await res.json().catch(() => ({}))
  const data = raw?.code !== undefined ? (raw.code === 0 ? raw.data : raw) : raw
  if (!res.ok || raw?.code > 0) {
    const err = new Error(raw.message || raw.error || '请求失败')
    err.reason = raw.reason || ''
    err.metadata = raw.metadata || null
    throw err
  }
  return data
}

export function apiErrorCode(err) {
  return err && typeof err === 'object' ? String(err.reason || err.code || '') : ''
}

// ---- Markdown (announcements / payment help) ----
marked.setOptions({ gfm: true, breaks: true })
export function renderMarkdown(text) {
  return DOMPurify.sanitize(marked.parse(String(text || '')))
}

// ---- Device detection ----
export function isMobileDevice() {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent || ''
  return /android|iphone|ipad|ipod|opera mini|iemobile|mobile/i.test(ua)
    || (navigator.maxTouchPoints > 1 && /Macintosh/.test(ua))
}
export function isWechatBrowser() {
  return typeof navigator !== 'undefined' && /MicroMessenger/i.test(navigator.userAgent || '')
}

// ---- Payment method normalization (frontend/src/components/payment/paymentFlow.ts) ----
const VISIBLE_METHOD_ALIASES = { alipay: 'alipay', alipay_direct: 'alipay', wxpay: 'wxpay', wxpay_direct: 'wxpay', stripe: 'stripe', airwallex: 'airwallex' }
export const METHOD_ORDER = ['alipay', 'alipay_direct', 'wxpay', 'wxpay_direct', 'stripe', 'airwallex']
export const METHOD_LABELS = { alipay: '支付宝', wxpay: '微信支付', stripe: 'Stripe（银行卡等）', airwallex: 'Airwallex' }
export function normalizeVisibleMethod(method) {
  const key = String(method || '').trim()
  return VISIBLE_METHOD_ALIASES[key] || ''
}
export function getVisibleMethods(methods) {
  const visible = {}
  Object.entries(methods || {}).forEach(([type, limit]) => {
    const normalized = normalizeVisibleMethod(type) || type.trim()
    if (!normalized) return
    if (!visible[normalized] || type === normalized) visible[normalized] = { ...limit }
  })
  return visible
}
export function sortMethodEntries(entries) {
  return entries.slice().sort((a, b) => {
    const ia = METHOD_ORDER.indexOf(a[0]); const ib = METHOD_ORDER.indexOf(b[0])
    return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib)
  })
}

// ---- Currency / amounts (frontend/src/components/payment/currency.ts) ----
export function currencySymbol(currency) {
  const map = { CNY: '¥', RMB: '¥', USD: '$', EUR: '€', GBP: '£', JPY: '¥', HKD: 'HK$', TWD: 'NT$', KRW: '₩' }
  const key = String(currency || '').trim().toUpperCase()
  return map[key] || (key || '¥')
}
export function formatPaymentAmount(amount, currency) {
  const n = Number(amount)
  if (!Number.isFinite(n)) return `${currencySymbol(currency)}0.00`
  return `${currencySymbol(currency)}${n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}
export function feeAmountFor(amount, feeRate) { return Math.ceil(Number(amount || 0) * Number(feeRate || 0) / 100) }
export function totalAmountFor(amount, feeRate) { return Math.round(Number(amount || 0) + feeAmountFor(amount, feeRate)) }

// ---- Order creation payload / launch decision (paymentFlow.ts port) ----
export function buildCreateOrderPayload(input) {
  const visibleMethod = normalizeVisibleMethod(input.paymentType) || String(input.paymentType || '').trim()
  const origin = String(input.origin || location.origin).replace(/\/+$/, '')
  const forceQRCode = !!input.forceQRCode
  const mobilePrecreateDeepLink = !!input.mobilePrecreateDeepLink
  const effectiveMobile = (forceQRCode && !mobilePrecreateDeepLink && visibleMethod === 'alipay') ? false : input.isMobile
  const payload = {
    amount: input.amount,
    payment_type: visibleMethod,
    order_type: input.orderType,
    is_mobile: effectiveMobile,
    payment_source: visibleMethod === 'wxpay' && input.isWechatBrowser ? 'wechat_in_app_resume' : 'hosted_redirect',
  }
  if (input.planId) payload.plan_id = input.planId
  payload.return_url = `${origin}/recharge/result`
  return payload
}

export const PAYMENT_RECOVERY_STORAGE_KEY = 'payment.recovery.current'
export function createPaymentRecoverySnapshot(state, now = Date.now()) { return { ...state, createdAt: now } }
export function writePaymentRecoverySnapshot(snapshot) {
  try { localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify(snapshot)) } catch { /* storage unavailable */ }
}
export function clearPaymentRecoverySnapshot() {
  try { localStorage.removeItem(PAYMENT_RECOVERY_STORAGE_KEY) } catch { /* storage unavailable */ }
}
export function readPaymentRecoverySnapshot(options = {}) {
  let raw = null
  try { raw = localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY) } catch { return null }
  if (!raw) return null
  try {
    const p = JSON.parse(raw)
    if (typeof p.orderId !== 'number' || typeof p.amount !== 'number' || typeof p.paymentType !== 'string' || typeof p.createdAt !== 'number') return null
    const expiresAt = Date.parse(p.expiresAt)
    if (Number.isFinite(expiresAt) && expiresAt <= (options.now || Date.now())) return null
    if (options.resumeToken && p.resumeToken !== options.resumeToken) return null
    return {
      orderId: p.orderId, amount: p.amount, qrCode: p.qrCode || '', expiresAt: p.expiresAt || '',
      paymentType: p.paymentType, payUrl: p.payUrl || '', outTradeNo: p.outTradeNo || '',
      clientSecret: p.clientSecret || '', intentId: p.intentId || '', currency: p.currency || '',
      countryCode: p.countryCode || '', paymentEnv: p.paymentEnv || '', payAmount: p.payAmount ?? 0,
      orderType: p.orderType === 'subscription' ? 'subscription' : 'balance', paymentMode: p.paymentMode || '',
      resumeToken: p.resumeToken || '', alipayMobilePrecreateDeepLink: p.alipayMobilePrecreateDeepLink === true,
      createdAt: p.createdAt,
    }
  } catch { return null }
}

// Launch kinds: qr_waiting / alipay_deep_link / redirect_waiting / stripe_popup /
// stripe_route / airwallex_route / wechat_oauth / wechat_jsapi / unhandled
export function decidePaymentLaunch(result, context) {
  const visibleMethod = normalizeVisibleMethod(context.visibleMethod) || context.visibleMethod
  const baseState = createPaymentRecoverySnapshot({
    orderId: result.order_id,
    amount: result.amount,
    qrCode: result.qr_code || '',
    expiresAt: result.expires_at || '',
    paymentType: visibleMethod,
    payUrl: result.pay_url || '',
    outTradeNo: result.out_trade_no || '',
    clientSecret: result.client_secret || '',
    intentId: result.intent_id || '',
    currency: result.currency || '',
    countryCode: result.country_code || '',
    paymentEnv: result.payment_env || '',
    payAmount: result.pay_amount ?? 0,
    orderType: context.orderType,
    paymentMode: String(result.payment_mode || '').trim(),
    resumeToken: result.resume_token || '',
    alipayMobilePrecreateDeepLink: result.alipay_mobile_precreate_deep_link === true,
  })
  if (visibleMethod === 'airwallex' && baseState.clientSecret && baseState.intentId) {
    if (!context.airwallexRouteUrl) return { kind: 'unhandled', paymentState: baseState }
    return { kind: 'airwallex_route', paymentState: { ...baseState, payUrl: context.airwallexRouteUrl } }
  }
  if (baseState.clientSecret) {
    const isStripeButton = visibleMethod === 'stripe'
    const stripeMethod = isStripeButton ? undefined : (visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay')
    const kind = stripeMethod === 'alipay' && !context.isMobile ? 'stripe_popup' : 'stripe_route'
    return { kind, paymentState: { ...baseState, payUrl: kind === 'stripe_popup' ? context.stripePopupUrl || '' : context.stripeRouteUrl || '' }, stripeMethod }
  }
  if (result.result_type === 'oauth_required' && result.oauth?.authorize_url) {
    return { kind: 'wechat_oauth', paymentState: baseState, oauth: result.oauth }
  }
  const jsapiPayload = result.jsapi || result.jsapi_payload
  if (result.result_type === 'jsapi_ready' && jsapiPayload) {
    return { kind: 'wechat_jsapi', paymentState: baseState, jsapi: jsapiPayload }
  }
  if (visibleMethod === 'alipay' && context.isMobile && baseState.alipayMobilePrecreateDeepLink && baseState.qrCode) {
    return { kind: 'alipay_deep_link', paymentState: baseState }
  }
  const forceQRCode = !!context.forceQRCode && !context.mobilePrecreateDeepLink && visibleMethod === 'alipay'
  const effectiveMobile = forceQRCode ? false : context.isMobile
  const mode = baseState.paymentMode.toLowerCase()
  const prefersRedirect = mode === 'redirect' || mode === 'popup' || (effectiveMobile && !!baseState.payUrl)
  const prefersQr = mode === 'qrcode' || mode === 'native' || (!prefersRedirect && !!baseState.qrCode)
  if (visibleMethod === 'wxpay' && context.isWechatBrowser && baseState.payUrl && !baseState.qrCode) {
    return { kind: 'redirect_waiting', paymentState: baseState }
  }
  if (prefersRedirect && baseState.payUrl) return { kind: 'redirect_waiting', paymentState: baseState }
  if (prefersQr && baseState.qrCode) return { kind: 'qr_waiting', paymentState: baseState }
  if (baseState.payUrl) return { kind: 'redirect_waiting', paymentState: baseState }
  return { kind: 'unhandled', paymentState: baseState }
}

// ---- Stripe route URL (same query contract as the original frontend) ----
export function buildStripeRouteUrl({ orderId, clientSecret, method, resumeToken }) {
  const params = new URLSearchParams({ order_id: String(orderId), client_secret: clientSecret })
  if (method !== undefined) params.set('method', method)
  if (resumeToken) params.set('resume_token', resumeToken)
  return `/payment/stripe?${params.toString()}`
}
export function buildAirwallexRouteUrl({ orderId, outTradeNo, resumeToken }) {
  const params = new URLSearchParams({ order_id: String(orderId) })
  if (outTradeNo) params.set('out_trade_no', outTradeNo)
  if (resumeToken) params.set('resume_token', resumeToken)
  return `/payment/airwallex?${params.toString()}`
}

// ---- Alipay mobile deep link (frontend/src/components/payment/alipayDeepLink.ts) ----
export function buildAlipayDeepLink(qrCode) {
  const qrcode = String(qrCode || '').trim()
  return qrcode ? `alipays://platformapi/startapp?saId=10000007&qrcode=${encodeURIComponent(qrcode)}` : ''
}

// ---- Misc ----
export function formatPaymentDate(value) {
  if (!value) return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
export function relativeTime(value) {
  const t = new Date(value).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  if (diff < 60000) return '刚刚'
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`
  if (diff < 2592000000) return `${Math.floor(diff / 86400000)} 天前`
  return new Date(t).toLocaleDateString('zh-CN')
}
export const ORDER_STATUS_LABELS = {
  PENDING: '待支付', PAID: '已支付', RECHARGING: '到账中', COMPLETED: '已完成', EXPIRED: '已过期',
  CANCELLED: '已取消', FAILED: '失败', REFUND_REQUESTED: '退款申请中', REFUNDING: '退款中',
  REFUND_PENDING: '退款待处理', PARTIALLY_REFUNDED: '部分退款', REFUNDED: '已退款', REFUND_FAILED: '退款失败',
}
export const SUCCESS_ORDER_STATUSES = ['COMPLETED', 'PAID', 'RECHARGING']
export function isTerminalSuccess(status) { return SUCCESS_ORDER_STATUSES.includes(String(status || '').toUpperCase()) }
